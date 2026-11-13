// Package polybft implements PBFT consensus algorithm integration and bridge feature
package polybft

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/hashicorp/go-hclog"
	bolt "go.etcd.io/bbolt"

	"github.com/0xPolygon/polygon-edge/blockchain"
	"github.com/0xPolygon/polygon-edge/chain"
	"github.com/0xPolygon/polygon-edge/consensus"
	"github.com/0xPolygon/polygon-edge/consensus/polybft/contractsapi"
	"github.com/0xPolygon/polygon-edge/consensus/polybft/signer"
	"github.com/0xPolygon/polygon-edge/consensus/polybft/validator"
	"github.com/0xPolygon/polygon-edge/consensus/polybft/wallet"
	"github.com/0xPolygon/polygon-edge/contracts"
	"github.com/0xPolygon/polygon-edge/forkmanager"
	"github.com/0xPolygon/polygon-edge/helper/common"
	"github.com/0xPolygon/polygon-edge/helper/progress"
	"github.com/0xPolygon/polygon-edge/network"
	"github.com/0xPolygon/polygon-edge/state"
	"github.com/0xPolygon/polygon-edge/syncer"
	"github.com/0xPolygon/polygon-edge/types"
)

const (
	minSyncPeers = 0 // standalone single-validator mode
	pbftProto    = "/pbft/0.2"
	bridgeProto  = "/bridge/0.2"

	// sequenceOvertakenGrace is how long the head may sit at or past the running sequence's height
	// before the sequence is treated as stuck. Longer than a block insert with its epoch hooks.
	sequenceOvertakenGrace = 5 * time.Second
)

var (
	errMissingBridgeConfig = errors.New("invalid genesis configuration, missing bridge configuration")
)

// polybftBackend is an interface defining polybft methods needed by fsm and sync tracker
type polybftBackend interface {
	// GetValidators retrieves validator set for the given block
	GetValidators(blockNumber uint64, parents []*types.Header) (validator.AccountSet, error)

	// GetValidators retrieves validator set for the given block
	// Function expects that db tx is already open
	GetValidatorsWithTx(blockNumber uint64, parents []*types.Header,
		dbTx *bolt.Tx) (validator.AccountSet, error)
}

// Factory is the factory function to create a discovery consensus
func Factory(params *consensus.Params) (consensus.Consensus, error) {
	logger := params.Logger.Named("polybft")

	setupHeaderHashFunc()

	polybft := &Polybft{
		config:  params,
		closeCh: make(chan struct{}),
		logger:  logger,
		txPool:  params.TxPool,
	}

	// initialize polybft consensus config
	customConfigJSON, err := json.Marshal(params.Config.Config)
	if err != nil {
		return nil, err
	}

	err = json.Unmarshal(customConfigJSON, &polybft.consensusConfig)
	if err != nil {
		return nil, err
	}

	return polybft, nil
}

type Polybft struct {
	// closeCh is used to signal that consensus protocol is stopped
	closeCh chan struct{}

	// ibft is the ibft engine
	ibft *IBFTConsensusWrapper

	// state is reference to the struct which encapsulates consensus data persistence logic
	state *State

	// consensus parameters
	config *consensus.Params

	// consensusConfig is genesis configuration for polybft consensus protocol
	consensusConfig *PolyBFTConfig

	// blockchain is a reference to the blockchain object
	blockchain blockchainBackend

	// runtime handles consensus runtime features like epoch, state and event management
	runtime *consensusRuntime

	// block time duration
	blockTime time.Duration

	// dataDir is the data directory to store the info
	dataDir string

	// reference to the syncer
	syncer syncer.Syncer

	// topic for consensus engine messages
	consensusTopic *network.Topic

	// topic for bridge messages
	bridgeTopic *network.Topic

	// key encapsulates ECDSA address and BLS signing logic
	key *wallet.Key

	// validatorsCache represents cache of validators snapshots
	validatorsCache *validatorsSnapshotCache

	// logger
	logger hclog.Logger

	// tx pool as interface
	txPool txPoolInterface
}

func GenesisPostHookFactory(config *chain.Chain, engineName string) func(txn *state.Transition) error {
	return func(transition *state.Transition) error {
		polyBFTConfig, err := GetPolyBFTConfig(config)
		if err != nil {
			return err
		}

		// calculate initial total supply of native erc20 token
		// we skip zero address, since its a special case address
		// that is used for minting and burning native token
		initialTotalSupply := big.NewInt(0)

		for addr, alloc := range config.Genesis.Alloc {
			if addr == types.ZeroAddress {
				continue
			}

			initialTotalSupply.Add(initialTotalSupply, alloc.Balance)
		}

		bridgeCfg := polyBFTConfig.Bridge
		// Standalone mode: skip bridge-dependent initialization if no bridge configured
		if bridgeCfg == nil {
			hclog.L().Warn("No bridge configuration found. Running in standalone mode (no rootchain).")
		}

		proxyAddrMapping := contracts.GetProxyImplementationMapping()

		burnContractAddress, isBurnContractSet := getBurnContractAddress(config, polyBFTConfig)
		if isBurnContractSet {
			proxyAddrMapping[contracts.DefaultBurnContract] = burnContractAddress
		}

		if _, ok := config.Genesis.Alloc[contracts.RewardTokenContract]; ok {
			proxyAddrMapping[contracts.RewardTokenContract] = contracts.RewardTokenContractV1
		}

		if err = initProxies(transition, polyBFTConfig.ProxyContractsAdmin, proxyAddrMapping); err != nil {
			return err
		}

		// initialize ValidatorSet SC
		if err = initValidatorSet(polyBFTConfig, transition); err != nil {
			return err
		}

		// approve reward pool
		if err = approveRewardPoolAsSpender(polyBFTConfig, transition); err != nil {
			return err
		}

		// mint reward tokens to reward wallet
		if err = mintRewardTokensToWallet(polyBFTConfig, transition); err != nil {
			return err
		}

		// initialize RewardPool SC
		if err = initRewardPool(polyBFTConfig, transition); err != nil {
			return err
		}

		// check if there are Bridge Allow List Admins and Bridge Block List Admins
		// and if there are, get the first address as the Admin
		// Skip bridge predicate initialization in standalone mode
		if bridgeCfg != nil {
			bridgeAllowListAdmin := types.ZeroAddress
			if config.Params.BridgeAllowList != nil && len(config.Params.BridgeAllowList.AdminAddresses) > 0 {
				bridgeAllowListAdmin = config.Params.BridgeAllowList.AdminAddresses[0]
			}

			bridgeBlockListAdmin := types.ZeroAddress
			if config.Params.BridgeBlockList != nil && len(config.Params.BridgeBlockList.AdminAddresses) > 0 {
				bridgeBlockListAdmin = config.Params.BridgeBlockList.AdminAddresses[0]
			}

			// initialize Predicate SCs
			if bridgeAllowListAdmin != types.ZeroAddress || bridgeBlockListAdmin != types.ZeroAddress {
				// The owner of the contract will be the allow list admin or the block list admin, if any of them is set.
				owner := contracts.SystemCaller
				useBridgeAllowList := bridgeAllowListAdmin != types.ZeroAddress
				useBridgeBlockList := bridgeBlockListAdmin != types.ZeroAddress

				if bridgeAllowListAdmin != types.ZeroAddress {
					owner = bridgeAllowListAdmin
				} else if bridgeBlockListAdmin != types.ZeroAddress {
					owner = bridgeBlockListAdmin
				}

				// initialize ChildERC20PredicateAccessList SC
				input, err := getInitERC20PredicateACLInput(polyBFTConfig.Bridge, owner,
					useBridgeAllowList, useBridgeBlockList, false)
				if err != nil {
					return err
				}

				if err = callContract(contracts.SystemCaller, contracts.ChildERC20PredicateContract, input,
					"ChildERC20PredicateAccessList", transition); err != nil {
					return err
				}

				// initialize ChildERC721PredicateAccessList SC
				input, err = getInitERC721PredicateACLInput(polyBFTConfig.Bridge, owner,
					useBridgeAllowList, useBridgeBlockList, false)
				if err != nil {
					return err
				}

				if err = callContract(contracts.SystemCaller, contracts.ChildERC721PredicateContract, input,
					"ChildERC721PredicateAccessList", transition); err != nil {
					return err
				}

				// initialize ChildERC1155PredicateAccessList SC
				input, err = getInitERC1155PredicateACLInput(polyBFTConfig.Bridge, owner,
					useBridgeAllowList, useBridgeBlockList, false)
				if err != nil {
					return err
				}

				if err = callContract(contracts.SystemCaller, contracts.ChildERC1155PredicateContract, input,
					"ChildERC1155PredicateAccessList", transition); err != nil {
					return err
				}

				// initialize RootMintableERC20PredicateAccessList SC
				input, err = getInitERC20PredicateACLInput(polyBFTConfig.Bridge, owner,
					useBridgeAllowList, useBridgeBlockList, true)
				if err != nil {
					return err
				}

				if err = callContract(contracts.SystemCaller, contracts.RootMintableERC20PredicateContract, input,
					"RootMintableERC20PredicateAccessList", transition); err != nil {
					return err
				}

				// initialize RootMintableERC721PredicateAccessList SC
				input, err = getInitERC721PredicateACLInput(polyBFTConfig.Bridge, owner,
					useBridgeAllowList, useBridgeBlockList, true)
				if err != nil {
					return err
				}

				if err = callContract(contracts.SystemCaller, contracts.RootMintableERC721PredicateContract, input,
					"RootMintableERC721PredicateAccessList", transition); err != nil {
					return err
				}

				// initialize RootMintableERC1155PredicateAccessList SC
				input, err = getInitERC1155PredicateACLInput(polyBFTConfig.Bridge, owner,
					useBridgeAllowList, useBridgeBlockList, true)
				if err != nil {
					return err
				}

				if err = callContract(contracts.SystemCaller, contracts.RootMintableERC1155PredicateContract, input,
					"RootMintableERC1155PredicateAccessList", transition); err != nil {
					return err
				}
			} else {
				// initialize ChildERC20Predicate SC
				input, err := getInitERC20PredicateInput(bridgeCfg, false)
				if err != nil {
					return err
				}

				if err = callContract(contracts.SystemCaller, contracts.ChildERC20PredicateContract, input,
					"ChildERC20Predicate", transition); err != nil {
					return err
				}

				// initialize ChildERC721Predicate SC
				input, err = getInitERC721PredicateInput(bridgeCfg, false)
				if err != nil {
					return err
				}

				if err = callContract(contracts.SystemCaller, contracts.ChildERC721PredicateContract, input,
					"ChildERC721Predicate", transition); err != nil {
					return err
				}

				// initialize ChildERC1155Predicate SC
				input, err = getInitERC1155PredicateInput(bridgeCfg, false)
				if err != nil {
					return err
				}

				if err = callContract(contracts.SystemCaller, contracts.ChildERC1155PredicateContract, input,
					"ChildERC1155Predicate", transition); err != nil {
					return err
				}

				// initialize RootMintableERC20Predicate SC
				input, err = getInitERC20PredicateInput(bridgeCfg, true)
				if err != nil {
					return err
				}

				if err = callContract(contracts.SystemCaller, contracts.RootMintableERC20PredicateContract, input,
					"RootMintableERC20Predicate", transition); err != nil {
					return err
				}

				// initialize RootMintableERC721Predicate SC
				input, err = getInitERC721PredicateInput(bridgeCfg, true)
				if err != nil {
					return err
				}

				if err = callContract(contracts.SystemCaller, contracts.RootMintableERC721PredicateContract, input,
					"RootMintableERC721Predicate", transition); err != nil {
					return err
				}

				// initialize RootMintableERC1155Predicate SC
				input, err = getInitERC1155PredicateInput(bridgeCfg, true)
				if err != nil {
					return err
				}

				if err = callContract(contracts.SystemCaller, contracts.RootMintableERC1155PredicateContract, input,
					"RootMintableERC1155Predicate", transition); err != nil {
					return err
				}
			}
		} // end bridgeCfg != nil

		if polyBFTConfig.NativeTokenConfig.IsMintable {
			// initialize NativeERC20Mintable SC
			params := &contractsapi.InitializeNativeERC20MintableFn{
				Predicate_:   contracts.ChildERC20PredicateContract,
				Owner_:       polyBFTConfig.NativeTokenConfig.Owner,
				RootToken_:   types.ZeroAddress, // in case native mintable token is used, it is always root token
				Name_:        polyBFTConfig.NativeTokenConfig.Name,
				Symbol_:      polyBFTConfig.NativeTokenConfig.Symbol,
				Decimals_:    polyBFTConfig.NativeTokenConfig.Decimals,
				TokenSupply_: initialTotalSupply,
			}

			input, err := params.EncodeAbi()
			if err != nil {
				return err
			}

			if err = callContract(contracts.SystemCaller,
				contracts.NativeERC20TokenContract, input, "NativeERC20Mintable", transition); err != nil {
				return err
			}
		} else {
			// initialize NativeERC20 SC
			params := &contractsapi.InitializeNativeERC20Fn{
				Name_:        polyBFTConfig.NativeTokenConfig.Name,
				Symbol_:      polyBFTConfig.NativeTokenConfig.Symbol,
				Decimals_:    polyBFTConfig.NativeTokenConfig.Decimals,
				RootToken_:   polyBFTConfig.Bridge.RootNativeERC20Addr,
				Predicate_:   contracts.ChildERC20PredicateContract,
				TokenSupply_: initialTotalSupply,
			}

			input, err := params.EncodeAbi()
			if err != nil {
				return err
			}

			if err = callContract(contracts.SystemCaller,
				contracts.NativeERC20TokenContract, input, "NativeERC20", transition); err != nil {
				return err
			}

			// initialize EIP1559Burn SC
			if isBurnContractSet {
				burnParams := &contractsapi.InitializeEIP1559BurnFn{
					NewChildERC20Predicate: contracts.ChildERC20PredicateContract,
					NewBurnDestination:     config.Params.BurnContractDestinationAddress,
				}

				input, err = burnParams.EncodeAbi()
				if err != nil {
					return err
				}

				if err = callContract(contracts.SystemCaller,
					burnContractAddress,
					input, "EIP1559Burn", transition); err != nil {
					return err
				}
			}
		}

		return nil
	}
}

func ForkManagerFactory(forks *chain.Forks) error {
	// place fork manager handler registration here
	return nil
}

// Initialize initializes the consensus (e.g. setup data)
func (p *Polybft) Initialize() error {
	p.logger.Info("initializing polybft...")

	// read account
	account, err := wallet.NewAccountFromSecret(p.config.SecretsManager)
	if err != nil {
		return fmt.Errorf("failed to read account data. Error: %w", err)
	}

	// set key
	p.key = wallet.NewKey(account)

	// create and set syncer
	p.syncer = syncer.NewSyncer(
		p.config.Logger.Named("syncer"),
		p.config.Network,
		p.config.Blockchain,
		time.Duration(p.config.BlockTime)*3*time.Second,
	)

	// set blockchain backend
	p.blockchain = &blockchainWrapper{
		blockchain: p.config.Blockchain,
		executor:   p.config.Executor,
	}

	// create bridge and consensus topics
	if err = p.createTopics(); err != nil {
		return fmt.Errorf("cannot create topics: %w", err)
	}

	// set block time
	p.blockTime = time.Duration(p.config.BlockTime)

	// initialize polybft consensus data directory
	p.dataDir = filepath.Join(p.config.Config.Path, "polybft")
	// create the data dir if not exists
	if err = common.CreateDirSafe(p.dataDir, 0750); err != nil {
		return fmt.Errorf("failed to create data directory. Error: %w", err)
	}

	stt, err := newState(filepath.Join(p.dataDir, stateFileName), p.logger, p.closeCh)
	if err != nil {
		return fmt.Errorf("failed to create state instance. Error: %w", err)
	}

	p.state = stt
	p.validatorsCache = newValidatorsSnapshotCache(p.config.Logger, stt, p.blockchain)

	// create runtime
	if err := p.initRuntime(); err != nil {
		return err
	}

	p.ibft = newIBFTConsensusWrapper(p.logger, p.runtime, p)

	if err = p.subscribeToIbftTopic(); err != nil {
		return fmt.Errorf("IBFT topic subscription failed: %w", err)
	}

	return nil
}

func ForkManagerInitialParamsFactory(config *chain.Chain) (*forkmanager.ForkParams, error) {
	pbftConfig, err := GetPolyBFTConfig(config)
	if err != nil {
		return nil, err
	}

	return &forkmanager.ForkParams{
		MaxValidatorSetSize: &pbftConfig.MaxValidatorSetSize,
		EpochSize:           &pbftConfig.EpochSize,
		SprintSize:          &pbftConfig.SprintSize,
		BlockTime:           &pbftConfig.BlockTime,
		BlockTimeDrift:      &pbftConfig.BlockTimeDrift,
	}, nil
}

// Start starts the consensus and servers
func (p *Polybft) Start() error {
	p.logger.Info("starting polybft consensus", "signer", p.key.String())

	// start syncer (also initializes peer map)
	if err := p.syncer.Start(); err != nil {
		return fmt.Errorf("failed to start syncer. Error: %w", err)
	}

	// sync concurrently, retrying indefinitely
	go common.RetryForever(context.Background(), time.Second, func(context.Context) error {
		blockHandler := func(b *types.FullBlock) bool {
			p.runtime.OnBlockInserted(b)

			return false
		}
		if err := p.syncer.Sync(blockHandler); err != nil {
			p.logger.Error("blocks synchronization failed", "error", err)

			return err
		}

		return nil
	})

	// start consensus runtime
	if err := p.startRuntime(); err != nil {
		return fmt.Errorf("consensus runtime start failed: %w", err)
	}

	// start state DB process
	go p.state.startStatsReleasing()

	// polybft rootchain metrics
	go p.publishRootchainMetrics(p.logger.Named("rootchain_metrics"))

	return nil
}

// initRuntime creates consensus runtime
func (p *Polybft) initRuntime() error {
	runtimeConfig := &runtimeConfig{
		PolyBFTConfig:         p.consensusConfig,
		Key:                   p.key,
		DataDir:               p.dataDir,
		State:                 p.state,
		blockchain:            p.blockchain,
		polybftBackend:        p,
		txPool:                p.txPool,
		bridgeTopic:           p.bridgeTopic,
		numBlockConfirmations: p.config.NumBlockConfirmations,
		consensusConfig:       p.config.Config,
	}

	runtime, err := newConsensusRuntime(p.logger, runtimeConfig)
	if err != nil {
		return err
	}

	p.runtime = runtime

	return nil
}

// startRuntime starts consensus runtime
func (p *Polybft) startRuntime() error {
	go p.startConsensusProtocol()

	return nil
}

func (p *Polybft) startConsensusProtocol() {
	// wait to have at least n peers connected. The 2 is just an initial heuristic value
	// Most likely we will parametrize this in the future.
	if !p.waitForNPeers() {
		return
	}

	p.logger.Debug("peers connected")

	newBlockSub := p.blockchain.SubscribeEvents()
	defer p.blockchain.UnubscribeEvents(newBlockSub)

	// Buffered and never waited on: see forwardSyncerBlocks.
	syncerBlockCh := make(chan struct{}, 1)

	go forwardSyncerBlocks(p.closeCh, newBlockSub.GetEventCh(),
		func() uint64 { return p.blockchain.CurrentHeader().Number }, syncerBlockCh, p.logger)

	var (
		sequenceCh   <-chan struct{}
		stopSequence func()
		// runningHeight is the height of the live sequence, zero when there is none.
		runningHeight atomic.Uint64
	)

	// Buffered so the watcher never waits for this loop: a signal that waited through a long
	// stopSequence would land on the next, healthy sequence.
	overtakenCh := make(chan uint64, 1)

	go watchOvertakenSequence(p.closeCh, &runningHeight,
		func() uint64 { return p.blockchain.CurrentHeader().Number },
		overtakenCh, sequenceOvertakenGrace, p.logger)

	for {
		latestHeader := p.blockchain.CurrentHeader()

		currentValidators, err := p.GetValidators(latestHeader.Number, nil)
		if err != nil {
			p.logger.Error("failed to query current validator set", "block number", latestHeader.Number, "error", err)
		}

		isValidator := currentValidators.ContainsNodeID(p.key.String())
		p.runtime.setIsActiveValidator(isValidator)

		p.txPool.SetSealing(isValidator) // update tx pool

		runningHeight.Store(0)

		if isValidator {
			// initialize FSM as a stateless ibft backend via runtime as an adapter
			err = p.runtime.FSM()
			if err != nil {
				p.logger.Error("failed to create fsm", "block number", latestHeader.Number, "error", err)

				continue
			}

			sequenceCh, stopSequence = p.ibft.runSequence(latestHeader.Number + 1)
			runningHeight.Store(latestHeader.Number + 1)
		}

		now := time.Now().UTC()

	wait:
		for {
			select {
			case <-syncerBlockCh:
				if isValidator {
					stopSequence()
					p.logger.Info("canceled sequence", "sequence", latestHeader.Number+1)
				}

				break wait
			case height := <-overtakenCh:
				if isValidator && overtakenIsCurrent(height, latestHeader.Number) {
					stopSequence()
					p.logger.Info("canceled sequence", "sequence", height)

					break wait
				}
			case <-sequenceCh:
				break wait
			case <-p.closeCh:
				if isValidator {
					stopSequence()
				}

				return
			}
		}

		p.logger.Debug("time to run the sequence", "seconds", time.Since(now))
	}
}

// forwardSyncerBlocks tells the consensus loop that the syncer wrote a block, without ever waiting
// for it.
//
// The send used to block on an unbuffered channel. While the loop sat in stopSequence, waiting for a
// sequence whose runFin wanted the blockchain's write lock, this goroutine stopped draining its
// subscription; five more syncer blocks filled it, and the syncer's next WriteBlock blocked in
// dispatchEvent while holding that write lock — the sequence, the loop and the syncer all waiting on
// each other, the node frozen until a restart. Signals now coalesce: the loop only needs to know
// that one came, and it reads the head again itself.
func forwardSyncerBlocks(
	closeCh <-chan struct{},
	eventCh <-chan *blockchain.Event,
	head func() uint64,
	syncerBlockCh chan<- struct{},
	logger hclog.Logger,
) {
	for {
		select {
		case <-closeCh:
			return
		case ev := <-eventCh:
			// The blockchain notification system can eventually deliver
			// stale block notifications. These should be ignored
			if ev == nil || ev.Source != "syncer" || len(ev.NewChain) == 0 {
				continue
			}

			current := head()
			if ev.NewChain[0].Number < current {
				continue
			}

			logger.Info("sync block notification received", "block height", ev.NewChain[0].Number,
				"current height", current)

			select {
			case syncerBlockCh <- struct{}{}:
			default: // one is pending already; it covers this block too
			}
		}
	}
}

// watchOvertakenSequence cancels a sequence whose height is already on the chain.
//
// go-ibft can lose the end of a sequence: when the round timer fires while runFin writes the block,
// signalRoundDone is dropped and RunSequence moves to the next round of a height that is finished.
// The syncer then answers "block already inserted" and raises no event, so nothing ends that
// sequence. Two validators out of four caught like this leave no quorum for the next height, and
// the chain stops for good (31.10, block 8132285). The signal carries the height, the main loop
// drops it unless that height is still the one it runs.
func watchOvertakenSequence(
	closeCh <-chan struct{},
	running *atomic.Uint64,
	head func() uint64,
	cancelCh chan<- uint64,
	grace time.Duration,
	logger hclog.Logger,
) {
	ticker := time.NewTicker(grace / 5)
	defer ticker.Stop()

	var (
		overtaken uint64
		since     time.Time
	)

	for {
		select {
		case <-closeCh:
			return
		case now := <-ticker.C:
			height := running.Load()
			if height == 0 || head() < height {
				overtaken = 0

				continue
			}

			if overtaken != height {
				overtaken, since = height, now

				continue
			}

			if now.Sub(since) < grace {
				continue
			}

			logger.Warn("sequence overtaken by the chain head, canceling it", "height", height)

			overtaken = 0

			select {
			case cancelCh <- height:
			default: // one is pending already; the next look sends again if this height still stands
			}
		}
	}
}

// overtakenIsCurrent tells a signal for the running sequence from a stale one for an earlier height:
// the stale one must not cancel the live sequence.
func overtakenIsCurrent(height, latestHeight uint64) bool {
	return height == latestHeight+1
}

func (p *Polybft) waitForNPeers() bool {
	for {
		select {
		case <-p.closeCh:
			return false
		case <-time.After(2 * time.Second):
		}

		if len(p.config.Network.Peers()) >= minSyncPeers {
			break
		}
	}

	return true
}

// Close closes the connection
func (p *Polybft) Close() error {
	if p.syncer != nil {
		if err := p.syncer.Close(); err != nil {
			return err
		}
	}

	close(p.closeCh)
	p.runtime.close()

	return nil
}

// GetSyncProgression retrieves the current sync progression, if any
func (p *Polybft) GetSyncProgression() *progress.Progression {
	return p.syncer.GetSyncProgression()
}

// GetPeerHeights returns the latest block of each peer the syncer is tracking, keyed by
// peer id. The syncer keeps this to choose who to sync from, so this only surfaces what
// the node already observed. Nil before the syncer exists.
func (p *Polybft) GetPeerHeights() map[string]uint64 {
	if p.syncer == nil {
		return nil
	}

	return p.syncer.GetPeerHeights()
}

// VerifyHeader implements consensus.Engine and checks whether a header conforms to the consensus rules
func (p *Polybft) VerifyHeader(header *types.Header) error {
	// Short circuit if the header is known
	if _, ok := p.blockchain.GetHeaderByHash(header.Hash); ok {
		return nil
	}

	parent, ok := p.blockchain.GetHeaderByHash(header.ParentHash)
	if !ok {
		return fmt.Errorf(
			"unable to get parent header by hash for block number %d",
			header.Number,
		)
	}

	return p.verifyHeaderImpl(parent, header, p.consensusConfig.BlockTimeDrift, nil)
}

func (p *Polybft) verifyHeaderImpl(parent, header *types.Header, blockTimeDrift uint64, parents []*types.Header) error {
	// validate header fields
	if err := validateHeaderFields(parent, header, blockTimeDrift); err != nil {
		return fmt.Errorf("failed to validate header for block %d. error = %w", header.Number, err)
	}

	// decode the extra data
	extra, err := GetIbftExtra(header.ExtraData)
	if err != nil {
		return fmt.Errorf("failed to verify header for block %d. get extra error = %w", header.Number, err)
	}

	// validate extra data
	return extra.ValidateFinalizedData(
		header, parent, parents, p.blockchain.GetChainID(), p, signer.DomainCheckpointManager, p.logger)
}

func (p *Polybft) GetValidators(blockNumber uint64, parents []*types.Header) (validator.AccountSet, error) {
	return p.validatorsCache.GetSnapshot(blockNumber, parents, nil)
}

func (p *Polybft) GetValidatorsWithTx(blockNumber uint64, parents []*types.Header,
	dbTx *bolt.Tx) (validator.AccountSet, error) {
	return p.validatorsCache.GetSnapshot(blockNumber, parents, dbTx)
}

// ProcessHeaders updates the snapshot based on the verified headers
func (p *Polybft) ProcessHeaders(_ []*types.Header) error {
	// Not required
	return nil
}

// GetBlockCreator retrieves the block creator (or signer) given the block header
func (p *Polybft) GetBlockCreator(h *types.Header) (types.Address, error) {
	return types.BytesToAddress(h.Miner), nil
}

// PreCommitState a hook to be called before finalizing state transition on inserting block
func (p *Polybft) PreCommitState(block *types.Block, _ *state.Transition) error {
	commitmentTxExists := false

	validators, err := p.GetValidators(block.Number()-1, nil)
	if err != nil {
		return err
	}

	// validate commitment state transactions
	for _, tx := range block.Transactions {
		if tx.Type != types.StateTx {
			continue
		}

		decodedStateTx, err := decodeStateTransaction(tx.Input)
		if err != nil {
			return fmt.Errorf("unknown state transaction: tx=%v, error: %w", tx.Hash, err)
		}

		if signedCommitment, ok := decodedStateTx.(*CommitmentMessageSigned); ok {
			if commitmentTxExists {
				return fmt.Errorf("only one commitment state tx is allowed per block: %v", tx.Hash)
			}

			commitmentTxExists = true

			if err := verifyBridgeCommitmentTx(
				block.Number(),
				tx.Hash,
				signedCommitment,
				validator.NewValidatorSet(validators, p.logger)); err != nil {
				return err
			}
		}
	}

	return nil
}

// GetBridgeProvider is an implementation of Consensus interface
// Returns an instance of BridgeDataProvider
func (p *Polybft) GetBridgeProvider() consensus.BridgeDataProvider {
	return p.runtime
}

// FilterExtra is an implementation of Consensus interface
func (p *Polybft) FilterExtra(extra []byte) ([]byte, error) {
	return GetIbftExtraClean(extra)
}

// initProxies initializes proxy contracts, that allow upgradeability of contracts implementation
func initProxies(transition *state.Transition, admin types.Address,
	proxyToImplMap map[types.Address]types.Address) error {
	for proxyAddress, implAddress := range proxyToImplMap {
		protectSetupProxyFn := &contractsapi.ProtectSetUpProxyGenesisProxyFn{Initiator: contracts.SystemCaller}

		proxyInput, err := protectSetupProxyFn.EncodeAbi()
		if err != nil {
			return fmt.Errorf("GenesisProxy.protectSetUpProxy params encoding failed: %w", err)
		}

		err = callContract(contracts.SystemCaller, proxyAddress, proxyInput, "GenesisProxy.protectSetUpProxy", transition)
		if err != nil {
			return err
		}

		setUpproxyFn := &contractsapi.SetUpProxyGenesisProxyFn{
			Logic: implAddress,
			Admin: admin,
			Data:  []byte{},
		}

		proxyInput, err = setUpproxyFn.EncodeAbi()
		if err != nil {
			return fmt.Errorf("GenesisProxy.setUpProxy params encoding failed: %w", err)
		}

		err = callContract(contracts.SystemCaller, proxyAddress, proxyInput, "GenesisProxy.setUpProxy", transition)
		if err != nil {
			return err
		}
	}

	return nil
}

func getBurnContractAddress(config *chain.Chain, polyBFTConfig PolyBFTConfig) (types.Address, bool) {
	if config.Params.BurnContract != nil &&
		len(config.Params.BurnContract) == 1 &&
		!polyBFTConfig.NativeTokenConfig.IsMintable {
		for _, address := range config.Params.BurnContract {
			if _, ok := config.Genesis.Alloc[address]; ok {
				return address, true
			}
		}
	}

	return types.ZeroAddress, false
}

package polybft

import (
	"fmt"
	"math/big"
	"sort"

	"github.com/0xPolygon/polygon-edge/bls"
	"github.com/0xPolygon/polygon-edge/consensus/polybft/bitmap"
	"github.com/0xPolygon/polygon-edge/consensus/polybft/validator"
	"github.com/0xPolygon/polygon-edge/contracts"
	"github.com/0xPolygon/polygon-edge/types"
	"github.com/hashicorp/go-hclog"
	"github.com/umbracle/ethgo"
	"github.com/umbracle/ethgo/abi"
	"github.com/umbracle/ethgo/contract"
	bolt "go.etcd.io/bbolt"
)

const bpsDenominator = 10_000

var (
	// Read-only views on AetherionValidatorRegistry. Kept minimal on purpose: the node
	// only needs the active roster (address, proven BLS key, raw stake) and the current
	// voting-power ceiling.
	getActiveValidatorsMethod = abi.MustNewMethod(
		"function getActiveValidators() returns (address[] operators, bytes[] blsKeys, uint256[] stakes)")
	powerCapBpsMethod = abi.MustNewMethod("function powerCapBps() returns (uint256)")
)

var _ StakeManager = (*nativeStakeManager)(nil)

// nativeStakeManager builds the validator set each epoch directly from the on-chain
// AetherionValidatorRegistry, with no rootchain involved. It is the Aetherion staking
// fork's replacement for the dummy (frozen-set) stake manager on this bridge-less chain.
//
// Before AetherionStakingForkEpoch it returns an empty delta — behaving exactly like the
// frozen genesis set — so a node replaying history reproduces the pre-fork set until the
// boundary, then switches to the registry-driven set. Both branches are pure functions of
// epoch and on-chain state, which is what keeps resync deterministic.
type nativeStakeManager struct {
	logger              hclog.Logger
	blockchain          blockchainBackend
	registryAddr        types.Address
	maxValidatorSetSize int
}

func newNativeStakeManager(
	logger hclog.Logger,
	blockchain blockchainBackend,
	registryAddr types.Address,
	maxValidatorSetSize int,
) *nativeStakeManager {
	return &nativeStakeManager{
		logger:              logger,
		blockchain:          blockchain,
		registryAddr:        registryAddr,
		maxValidatorSetSize: maxValidatorSetSize,
	}
}

// PostBlock is a no-op: unlike the rootchain stake manager, the native manager holds no
// incrementally-tracked state — it reads the registry fresh at every epoch boundary.
func (n *nativeStakeManager) PostBlock(req *PostBlockRequest) error { return nil }

// GetLogFilters returns none: the native manager does not track events.
func (n *nativeStakeManager) GetLogFilters() map[types.Address][]types.Hash {
	return make(map[types.Address][]types.Hash)
}

// ProcessLog is a no-op for the same reason.
func (n *nativeStakeManager) ProcessLog(header *types.Header, log *ethgo.Log, dbTx *bolt.Tx) error {
	return nil
}

// UpdateValidatorSet returns the delta that moves the current set to the set defined by
// the registry (with the voting-power ceiling applied). Before the fork epoch it returns
// an empty delta (frozen set). On any registry read failure it also returns an empty
// delta rather than an error, so a transient read hiccup can never halt block building —
// the set simply stays unchanged for that epoch.
func (n *nativeStakeManager) UpdateValidatorSet(
	epoch uint64, oldValidatorSet validator.AccountSet) (*validator.ValidatorSetDelta, error) {
	if !contracts.IsAetherionStakingForkActive(epoch) {
		return &validator.ValidatorSetDelta{}, nil
	}

	newSet, err := n.readValidatorSet()
	if err != nil {
		n.logger.Warn("native stake manager: registry read failed, keeping current set",
			"epoch", epoch, "err", err)

		return &validator.ValidatorSetDelta{}, nil
	}

	// Never empty the validator set. If the registry reports no active validators (e.g.
	// it was activated before the genesis validator was registered, or a misconfiguration),
	// keep the current set rather than removing everyone — an empty set halts the chain.
	// The registry has its own "cannot remove the last validator" guardrail; this is the
	// consensus-side backstop for the same invariant.
	if len(newSet) == 0 {
		n.logger.Warn("native stake manager: registry returned no active validators, keeping current set",
			"epoch", epoch)

		return &validator.ValidatorSetDelta{}, nil
	}

	return buildValidatorSetDelta(oldValidatorSet, newSet), nil
}

// readValidatorSet queries the registry at the current chain head for the active roster
// and voting-power ceiling, then builds the capped validator set.
func (n *nativeStakeManager) readValidatorSet() (validator.AccountSet, error) {
	header := n.blockchain.CurrentHeader()

	provider, err := n.blockchain.GetStateProviderForBlock(header)
	if err != nil {
		return nil, fmt.Errorf("state provider: %w", err)
	}

	operators, blsKeys, stakes, err := n.callGetActiveValidators(provider)
	if err != nil {
		return nil, err
	}

	capBps, err := n.callPowerCapBps(provider)
	if err != nil {
		return nil, err
	}

	powers := applyPowerCap(stakes, capBps)

	set := make(validator.AccountSet, 0, len(operators))

	for i := range operators {
		blsKey, err := bls.UnmarshalPublicKey(blsKeys[i])
		if err != nil {
			// A registered key always passed on-chain proof-of-possession, so this should
			// not happen; if it somehow does, skip that one validator (deterministically)
			// rather than fail the whole set and stall the epoch.
			n.logger.Warn("native stake manager: skipping validator with unparsable BLS key",
				"address", operators[i])

			continue
		}

		set = append(set, &validator.ValidatorMetadata{
			Address:     operators[i],
			BlsKey:      blsKey,
			VotingPower: powers[i],
			IsActive:    true,
		})
	}

	// Deterministic order and size bound: sort by voting power (desc), then address
	// (asc) as a stable tie-break, and cap to the network's maximum set size.
	sort.SliceStable(set, func(i, j int) bool {
		cmp := set[i].VotingPower.Cmp(set[j].VotingPower)
		if cmp != 0 {
			return cmp > 0
		}

		return set[i].Address.String() < set[j].Address.String()
	})

	if n.maxValidatorSetSize > 0 && len(set) > n.maxValidatorSetSize {
		set = set[:n.maxValidatorSetSize]
	}

	return set, nil
}

func (n *nativeStakeManager) callGetActiveValidators(
	provider contract.Provider) ([]types.Address, [][]byte, []*big.Int, error) {
	raw, err := provider.Call(ethgo.Address(n.registryAddr), getActiveValidatorsMethod.ID(), &contract.CallOpts{})
	if err != nil {
		return nil, nil, nil, fmt.Errorf("getActiveValidators call: %w", err)
	}

	decodedRaw, err := getActiveValidatorsMethod.Outputs.Decode(raw)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("getActiveValidators decode: %w", err)
	}

	decoded, ok := decodedRaw.(map[string]interface{})
	if !ok {
		return nil, nil, nil, fmt.Errorf("getActiveValidators: unexpected output shape")
	}

	rawOps, ok := decoded["operators"].([]ethgo.Address)
	if !ok {
		return nil, nil, nil, fmt.Errorf("getActiveValidators: bad operators output")
	}

	blsKeys, ok := decoded["blsKeys"].([][]byte)
	if !ok {
		return nil, nil, nil, fmt.Errorf("getActiveValidators: bad blsKeys output")
	}

	stakes, ok := decoded["stakes"].([]*big.Int)
	if !ok {
		return nil, nil, nil, fmt.Errorf("getActiveValidators: bad stakes output")
	}

	if len(rawOps) != len(blsKeys) || len(rawOps) != len(stakes) {
		return nil, nil, nil, fmt.Errorf("getActiveValidators: mismatched array lengths")
	}

	operators := make([]types.Address, len(rawOps))
	for i, a := range rawOps {
		operators[i] = types.Address(a)
	}

	return operators, blsKeys, stakes, nil
}

func (n *nativeStakeManager) callPowerCapBps(provider contract.Provider) (*big.Int, error) {
	raw, err := provider.Call(ethgo.Address(n.registryAddr), powerCapBpsMethod.ID(), &contract.CallOpts{})
	if err != nil {
		return nil, fmt.Errorf("powerCapBps call: %w", err)
	}

	decodedRaw, err := powerCapBpsMethod.Outputs.Decode(raw)
	if err != nil {
		return nil, fmt.Errorf("powerCapBps decode: %w", err)
	}

	decoded, ok := decodedRaw.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("powerCapBps: unexpected output shape")
	}

	capBps, ok := decoded["0"].(*big.Int)
	if !ok {
		return nil, fmt.Errorf("powerCapBps: bad output")
	}

	return capBps, nil
}

// applyPowerCap clamps each validator's voting power so that no single validator exceeds
// `capBps` basis points of the TOTAL voting power. Clamping the largest reduces the total,
// which lowers the ceiling, so the correct threshold C is found in closed form rather than
// by iteration: with the top k validators clamped to C, C solves C = capBps/denom * (C*k +
// (sum of the un-clamped)), i.e. C = capBps * sumUnclamped / (denom − k*capBps). Scanning
// k in ascending order and taking the first k whose C leaves the (k+1)-th largest under
// the cap yields the exact, least-clamping solution in O(n log n).
//
// When there are too few validators to satisfy the ceiling (n * capBps < 100% — e.g. a
// lone validator, unavoidably 100%), the cap is infeasible; the best achievable is an
// equal split, so every validator is clamped down to the smallest stake. Raw stake still
// determines reward share elsewhere — this ceiling only bounds consensus weight, which is
// the whole point of allowing variable stake without concentrating power.
//
// Pure integer arithmetic on copies of the inputs, so every node computes the same result.
func applyPowerCap(stakes []*big.Int, capBps *big.Int) []*big.Int {
	powers := make([]*big.Int, len(stakes))
	for i := range stakes {
		powers[i] = new(big.Int).Set(stakes[i])
	}

	// No effective ceiling when unset or at/above 100%, or with no validators.
	if len(powers) == 0 || capBps == nil || capBps.Sign() <= 0 ||
		capBps.Cmp(big.NewInt(bpsDenominator)) >= 0 {
		return powers
	}

	denom := big.NewInt(bpsDenominator)

	// Descending copy to locate the clamp threshold; the output keeps the input order.
	sorted := make([]*big.Int, len(powers))
	copy(sorted, powers)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Cmp(sorted[j]) > 0 })

	total := new(big.Int)
	for _, p := range sorted {
		total.Add(total, p)
	}

	if total.Sign() == 0 {
		return powers
	}

	var threshold *big.Int

	prefix := new(big.Int) // sum of the top-k (clamped) stakes as we scan k upward
	for k := 0; k < len(sorted); k++ {
		// denom − k*capBps; once non-positive the cap is infeasible for this many
		// clamped validators (they alone would exceed 100%).
		denomMinus := new(big.Int).Sub(denom, new(big.Int).Mul(big.NewInt(int64(k)), capBps))
		if denomMinus.Sign() <= 0 {
			break
		}

		sumUnclamped := new(big.Int).Sub(total, prefix)      // sum of sorted[k:]
		candidate := new(big.Int).Mul(capBps, sumUnclamped)  // capBps * sumUnclamped
		candidate.Div(candidate, denomMinus)                 // floor division

		// Valid when the first un-clamped validator (sorted[k]) is at or below the cap,
		// so it is correctly left unclamped. Ascending k guarantees this is also the
		// least-clamping (largest C) solution.
		if sorted[k].Cmp(candidate) <= 0 {
			threshold = candidate

			break
		}

		prefix.Add(prefix, sorted[k])
	}

	// Infeasible cap: equalize by clamping everyone down to the smallest stake (the
	// max-total equal distribution). sorted is descending, so the last entry is smallest.
	if threshold == nil {
		threshold = new(big.Int).Set(sorted[len(sorted)-1])
	}

	for i := range powers {
		if powers[i].Cmp(threshold) > 0 {
			powers[i] = new(big.Int).Set(threshold)
		}
	}

	return powers
}

// buildValidatorSetDelta computes the Added / Updated / Removed delta that turns
// oldValidatorSet into newValidatorSet. Same shape as the rootchain stake manager's delta
// so the rest of consensus consumes it unchanged.
func buildValidatorSetDelta(
	oldValidatorSet, newValidatorSet validator.AccountSet) *validator.ValidatorSetDelta {
	newAddresses := make(map[types.Address]struct{}, len(newValidatorSet))
	for _, v := range newValidatorSet {
		newAddresses[v.Address] = struct{}{}
	}

	removed := bitmap.Bitmap{}
	oldByAddress := make(map[types.Address]*validator.ValidatorMetadata, len(oldValidatorSet))

	for i, v := range oldValidatorSet {
		oldByAddress[v.Address] = v
		if _, stays := newAddresses[v.Address]; !stays {
			removed.Set(uint64(i))
		}
	}

	updated := validator.AccountSet{}
	added := validator.AccountSet{}

	for _, nv := range newValidatorSet {
		if ov, exists := oldByAddress[nv.Address]; exists {
			if ov.VotingPower.Cmp(nv.VotingPower) != 0 {
				updated = append(updated, nv)
			}
		} else {
			added = append(added, nv)
		}
	}

	return &validator.ValidatorSetDelta{
		Added:   added,
		Updated: updated,
		Removed: removed,
	}
}

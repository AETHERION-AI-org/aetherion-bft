package polybft

import (
	"math"
	"math/big"
	"testing"

	"github.com/0xPolygon/polygon-edge/consensus/polybft/contractsapi"
	"github.com/0xPolygon/polygon-edge/consensus/polybft/validator"
	"github.com/0xPolygon/polygon-edge/contracts"
	"github.com/0xPolygon/polygon-edge/state"
	"github.com/0xPolygon/polygon-edge/types"
	"github.com/hashicorp/go-hclog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// The router fork has no FSM test of its own until now: its gating was unit-tested, but not the
// transaction every validator must build and verify identically. A mismatch there does not fail
// a build or a contract test — it stalls sync at the fork boundary. These mirror the emission
// fork's tests (fsm_aetherion_emission_test.go) and share its caveat: package-level state, so
// none of them may call t.Parallel().

// setAetherionRewardsRouterForkForTest pins the fork gate for the calling test and restores it.
// A zero address disables the fork whatever the epoch, so the same helper covers "inactive"
// independently of the values baked into the build.
func setAetherionRewardsRouterForkForTest(t *testing.T, router types.Address, forkEpoch uint64) {
	t.Helper()

	origEpoch := contracts.AetherionRewardsRouterForkEpoch
	origAddr := contracts.AetherionRewardsRouterContract

	contracts.AetherionRewardsRouterForkEpoch = forkEpoch
	contracts.AetherionRewardsRouterContract = router

	t.Cleanup(func() {
		contracts.AetherionRewardsRouterForkEpoch = origEpoch
		contracts.AetherionRewardsRouterContract = origAddr
	})
}

var testRewardsRouter = types.StringToAddress("0xAA77")

func activateAetherionRewardsRouterForkForTest(t *testing.T, forkEpoch uint64) {
	t.Helper()
	setAetherionRewardsRouterForkForTest(t, testRewardsRouter, forkEpoch)
}

func deactivateAetherionRewardsRouterForkForTest(t *testing.T) {
	t.Helper()
	setAetherionRewardsRouterForkForTest(t, types.ZeroAddress, math.MaxUint64)
}

func routerTestFSM(t *testing.T, epoch uint64) *fsm {
	t.Helper()

	validators := validator.NewTestValidators(t, 5)

	return &fsm{
		parent:                 &types.Header{Number: 1},
		isEndOfEpoch:           true,
		isEndOfSprint:          true,
		epochNumber:            epoch,
		validators:             validator.NewValidatorSet(validators.GetPublicIdentities(), hclog.NewNullLogger()),
		commitEpochInput:       createTestCommitEpochInput(t, 0, 10),
		distributeRewardsInput: createTestDistributeRewardsInput(t, 0, validators.GetPublicIdentities(), 10),
		logger:                 hclog.NewNullLogger(),
	}
}

func TestFSM_createDistributeEpochRewardsTx_TargetsRouterWithTheFullEpochReward(t *testing.T) {
	activateAetherionRewardsRouterForkForTest(t, 0)

	f := &fsm{parent: &types.Header{Number: 41}, epochNumber: 7}

	tx, err := f.createDistributeEpochRewardsTx()
	require.NoError(t, err)
	require.NotNil(t, tx.To)
	require.Equal(t, testRewardsRouter, *tx.To)
	require.Equal(t, types.StateTx, tx.Type)
	require.Equal(t, contracts.SystemCaller, tx.From)
	require.Equal(t, uint64(types.StateTransactionGasLimit), tx.Gas)

	decoded := &contractsapi.DistributeEpochRewardsFn{}
	require.NoError(t, decoded.DecodeAbi(tx.Input))
	require.Equal(t, new(big.Int).SetUint64(7), decoded.EpochID)
	require.Equal(t, state.RawAetherionEpochReward(7), decoded.Reward)
}

// The transaction must decode as the router's, not as the validator payout that shares the
// state-transaction dispatch — the collision the method name was chosen to avoid.
func TestFSM_DistributeEpochRewardsTx_DecodesAsItself(t *testing.T) {
	activateAetherionRewardsRouterForkForTest(t, 0)

	tx, err := (&fsm{parent: &types.Header{Number: 1}, epochNumber: 3}).createDistributeEpochRewardsTx()
	require.NoError(t, err)

	decoded, err := decodeStateTransaction(tx.Input)
	require.NoError(t, err)
	_, ok := decoded.(*contractsapi.DistributeEpochRewardsFn)
	require.True(t, ok, "decoded as %T", decoded)
}

func TestFSM_applyDistributeEpochRewardsTx_NoopWhenForkInactive(t *testing.T) {
	deactivateAetherionRewardsRouterForkForTest(t)

	mBlockBuilder := new(blockBuilderMock)
	f := &fsm{parent: &types.Header{Number: 1}, epochNumber: 0, blockBuilder: mBlockBuilder}

	require.NoError(t, f.applyDistributeEpochRewardsTx())
	mBlockBuilder.AssertNotCalled(t, "WriteTx", mock.Anything)
}

func TestFSM_applyDistributeEpochRewardsTx_NoopBeforeTheForkEpoch(t *testing.T) {
	activateAetherionRewardsRouterForkForTest(t, 100)

	mBlockBuilder := new(blockBuilderMock)
	f := &fsm{parent: &types.Header{Number: 1}, epochNumber: 99, blockBuilder: mBlockBuilder}

	require.NoError(t, f.applyDistributeEpochRewardsTx())
	mBlockBuilder.AssertNotCalled(t, "WriteTx", mock.Anything)
}

func TestFSM_applyDistributeEpochRewardsTx_WritesFromTheForkEpoch(t *testing.T) {
	activateAetherionRewardsRouterForkForTest(t, 100)

	mBlockBuilder := new(blockBuilderMock)
	mBlockBuilder.On("WriteTx", mock.Anything).Return(error(nil)).Once()
	f := &fsm{parent: &types.Header{Number: 1}, epochNumber: 100, blockBuilder: mBlockBuilder}

	require.NoError(t, f.applyDistributeEpochRewardsTx())
	mBlockBuilder.AssertExpectations(t)
}

func TestFSM_verifyDistributeEpochRewardsTx(t *testing.T) {
	activateAetherionRewardsRouterForkForTest(t, 0)

	f := &fsm{isEndOfEpoch: true, epochNumber: 3, parent: &types.Header{Number: 1}}

	tx, err := f.createDistributeEpochRewardsTx()
	require.NoError(t, err)
	require.NoError(t, f.verifyDistributeEpochRewardsTx(tx))

	// another epoch encodes another reward schedule position: the hash must not match
	tampered, err := (&fsm{isEndOfEpoch: true, epochNumber: 4, parent: &types.Header{Number: 1}}).
		createDistributeEpochRewardsTx()
	require.NoError(t, err)
	assert.ErrorIs(t, f.verifyDistributeEpochRewardsTx(tampered), errEpochRewardsTxAmountMismatch)

	// a forged reward for the right epoch
	forgedInput, err := (&contractsapi.DistributeEpochRewardsFn{
		EpochID: big.NewInt(3),
		Reward:  new(big.Int).Add(state.RawAetherionEpochReward(3), big.NewInt(1)),
	}).EncodeAbi()
	require.NoError(t, err)
	forged := createStateTransactionWithData(f.Height(), testRewardsRouter, forgedInput)
	assert.ErrorIs(t, f.verifyDistributeEpochRewardsTx(forged), errEpochRewardsTxAmountMismatch)

	f.isEndOfEpoch = false
	assert.ErrorIs(t, f.verifyDistributeEpochRewardsTx(tx), errEpochRewardsTxNotExpected)
}

func TestFSM_verifyDistributeEpochRewardsTx_RejectsBeforeForkActive(t *testing.T) {
	deactivateAetherionRewardsRouterForkForTest(t)

	f := &fsm{isEndOfEpoch: true, epochNumber: 3, parent: &types.Header{Number: 1}}
	input, err := (&contractsapi.DistributeEpochRewardsFn{EpochID: big.NewInt(3), Reward: big.NewInt(1)}).EncodeAbi()
	require.NoError(t, err)

	tx := createStateTransactionWithData(f.Height(), types.StringToAddress("0xdead"), input)
	assert.ErrorIs(t, f.verifyDistributeEpochRewardsTx(tx), errEpochRewardsTxForkNotActive)
}

func TestFSM_VerifyStateTransactions_RouterForkActive_AllPass(t *testing.T) {
	activateAetherionRewardsRouterForkForTest(t, 0)

	f := routerTestFSM(t, 0)

	commitEpochTx, err := f.createCommitEpochTx()
	require.NoError(t, err)
	distributeRewardsTx, err := f.createDistributeRewardsTx()
	require.NoError(t, err)
	epochRewardsTx, err := f.createDistributeEpochRewardsTx()
	require.NoError(t, err)

	require.NoError(t, f.VerifyStateTransactions(
		[]*types.Transaction{commitEpochTx, distributeRewardsTx, epochRewardsTx}))
}

func TestFSM_VerifyStateTransactions_RouterForkActive_MissingTxRejected(t *testing.T) {
	activateAetherionRewardsRouterForkForTest(t, 0)

	f := routerTestFSM(t, 0)

	commitEpochTx, err := f.createCommitEpochTx()
	require.NoError(t, err)
	distributeRewardsTx, err := f.createDistributeRewardsTx()
	require.NoError(t, err)

	assert.ErrorIs(t,
		f.VerifyStateTransactions([]*types.Transaction{commitEpochTx, distributeRewardsTx}),
		errEpochRewardsTxDoesNotExist)
}

func TestFSM_VerifyStateTransactions_RouterForkInactive_TxRejected(t *testing.T) {
	deactivateAetherionRewardsRouterForkForTest(t)

	f := routerTestFSM(t, 0)

	commitEpochTx, err := f.createCommitEpochTx()
	require.NoError(t, err)
	distributeRewardsTx, err := f.createDistributeRewardsTx()
	require.NoError(t, err)

	forgedInput, err := (&contractsapi.DistributeEpochRewardsFn{EpochID: big.NewInt(0), Reward: big.NewInt(1)}).EncodeAbi()
	require.NoError(t, err)
	forged := createStateTransactionWithData(f.Height(), types.StringToAddress("0xdead"), forgedInput)

	err = f.VerifyStateTransactions([]*types.Transaction{commitEpochTx, distributeRewardsTx, forged})
	assert.ErrorContains(t, err, errEpochRewardsTxForkNotActive.Error())
}

func TestFSM_VerifyStateTransactions_RouterForkActive_DuplicateTxRejected(t *testing.T) {
	activateAetherionRewardsRouterForkForTest(t, 0)

	f := routerTestFSM(t, 0)

	commitEpochTx, err := f.createCommitEpochTx()
	require.NoError(t, err)
	distributeRewardsTx, err := f.createDistributeRewardsTx()
	require.NoError(t, err)
	epochRewardsTx, err := f.createDistributeEpochRewardsTx()
	require.NoError(t, err)

	err = f.VerifyStateTransactions(
		[]*types.Transaction{commitEpochTx, distributeRewardsTx, epochRewardsTx, epochRewardsTx})
	assert.ErrorIs(t, err, errEpochRewardsTxSingleExpected)
}

func TestFSM_BuildProposal_RouterForkActive_WritesTheRouterTx(t *testing.T) {
	const (
		accountCount      = 5
		committedCount    = 4
		parentCount       = 3
		parentBlockNumber = 1023
	)

	activateAetherionRewardsRouterForkForTest(t, 0)

	validators := validator.NewTestValidators(t, accountCount)
	extra := createTestExtra(validators.GetPublicIdentities(), validator.AccountSet{}, accountCount-1, committedCount, parentCount)

	parent := &types.Header{Number: parentBlockNumber, ExtraData: extra}
	parent.ComputeHash()
	stateBlock := createDummyStateBlock(parentBlockNumber+1, parent.Hash, extra)

	mBlockBuilder := newBlockBuilderMock(stateBlock)
	// commit epoch + distribute rewards + router; the emission and staking forks are not active
	// at epoch 0, so nothing else is written
	mBlockBuilder.On("WriteTx", mock.Anything).Return(error(nil)).Times(3)

	f := &fsm{parent: parent, blockBuilder: mBlockBuilder, config: &PolyBFTConfig{}, backend: new(blockchainMock),
		isEndOfEpoch:           true,
		validators:             validators.ToValidatorSet(),
		commitEpochInput:       createTestCommitEpochInput(t, 0, 10),
		distributeRewardsInput: createTestDistributeRewardsInput(t, 0, nil, 10),
		exitEventRootHash:      types.ZeroHash,
		logger:                 hclog.NewNullLogger(),
	}

	proposal, err := f.BuildProposal(0)
	assert.NoError(t, err)
	assert.NotNil(t, proposal)
	mBlockBuilder.AssertExpectations(t)
}

// A node resyncing from genesis must rebuild the byte-identical transaction for every epoch.
func TestFSM_RewardsRouterFork_ResyncDeterminism(t *testing.T) {
	activateAetherionRewardsRouterForkForTest(t, 5)

	build := func(epoch uint64) *types.Transaction {
		tx, err := (&fsm{parent: &types.Header{Number: 100}, epochNumber: epoch}).createDistributeEpochRewardsTx()
		require.NoError(t, err)

		return tx
	}

	require.Equal(t, build(5).Hash, build(5).Hash)
	require.NotEqual(t, build(5).Hash, build(6).Hash)
}

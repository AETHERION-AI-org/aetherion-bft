package polybft

import (
	"math/big"
	"testing"

	"github.com/0xPolygon/polygon-edge/consensus/polybft/contractsapi"
	"github.com/0xPolygon/polygon-edge/consensus/polybft/validator"
	"github.com/0xPolygon/polygon-edge/contracts"
	"github.com/0xPolygon/polygon-edge/types"
	"github.com/hashicorp/go-hclog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// activateAetherionEmissionForkForTest flips the package-level fork-gate vars on for
// the duration of the calling test and restores them afterward.
//
// Deliberately NOT safe to call from a t.Parallel() test: it mutates package-level
// state shared with every other test in this package. It is safe against the many
// t.Parallel() tests elsewhere in this file/package precisely because it does not
// call t.Parallel() itself — Go only resumes the body of a parallel test (the part
// after t.Parallel()) once every non-parallel top-level test, including this one, has
// finished running. See https://pkg.go.dev/testing#T.Parallel.
func activateAetherionEmissionForkForTest(t *testing.T, forkEpoch uint64) {
	t.Helper()

	origEpoch := contracts.AetherionEmissionForkEpoch
	origAddr := contracts.AetherionEmissionDistributorContract

	contracts.AetherionEmissionForkEpoch = forkEpoch
	contracts.AetherionEmissionDistributorContract = types.StringToAddress("0xEE55")

	t.Cleanup(func() {
		contracts.AetherionEmissionForkEpoch = origEpoch
		contracts.AetherionEmissionDistributorContract = origAddr
	})
}

func TestFSM_createDistributeEmissionTx_TargetsConfiguredDistributor(t *testing.T) {
	activateAetherionEmissionForkForTest(t, 0)

	fsm := &fsm{parent: &types.Header{Number: 41}, epochNumber: 7}

	tx, err := fsm.createDistributeEmissionTx()
	require.NoError(t, err)
	require.NotNil(t, tx.To)
	require.Equal(t, contracts.AetherionEmissionDistributorContract, *tx.To)
	require.Equal(t, types.StateTx, tx.Type)
	require.Equal(t, contracts.SystemCaller, tx.From)
	require.Equal(t, uint64(types.StateTransactionGasLimit), tx.Gas)

	decoded := &contractsapi.DistributeEmissionFn{}
	require.NoError(t, decoded.DecodeAbi(tx.Input))
	require.Equal(t, new(big.Int).SetUint64(7), decoded.EpochID)
}

func TestFSM_applyDistributeEmissionTx_NoopWhenForkInactive(t *testing.T) {
	// Fork left at its package default (disabled) deliberately.
	fsm := &fsm{parent: &types.Header{Number: 1}, epochNumber: 0}

	mBlockBuilder := new(blockBuilderMock)
	fsm.blockBuilder = mBlockBuilder

	require.NoError(t, fsm.applyDistributeEmissionTx())
	mBlockBuilder.AssertNotCalled(t, "WriteTx", mock.Anything)
}

func TestFSM_BuildProposal_EmissionForkActive_WritesThreeStateTxs(t *testing.T) {
	const (
		accountCount      = 5
		committedCount    = 4
		parentCount       = 3
		parentBlockNumber = 1023
		currentRound      = 0
	)

	activateAetherionEmissionForkForTest(t, 0)

	eventRoot := types.ZeroHash

	validators := validator.NewTestValidators(t, accountCount)
	extra := createTestExtra(validators.GetPublicIdentities(), validator.AccountSet{}, accountCount-1, committedCount, parentCount)

	parent := &types.Header{Number: parentBlockNumber, ExtraData: extra}
	parent.ComputeHash()
	stateBlock := createDummyStateBlock(parentBlockNumber+1, parent.Hash, extra)

	mBlockBuilder := newBlockBuilderMock(stateBlock)
	mBlockBuilder.On("WriteTx", mock.Anything).Return(error(nil)).Times(3)

	blockChainMock := new(blockchainMock)

	fsm := &fsm{parent: parent, blockBuilder: mBlockBuilder, config: &PolyBFTConfig{}, backend: blockChainMock,
		isEndOfEpoch:           true,
		validators:             validators.ToValidatorSet(),
		commitEpochInput:       createTestCommitEpochInput(t, 0, 10),
		distributeRewardsInput: createTestDistributeRewardsInput(t, 0, nil, 10),
		exitEventRootHash:      eventRoot,
		logger:                 hclog.NewNullLogger(),
	}

	proposal, err := fsm.BuildProposal(currentRound)
	assert.NoError(t, err)
	assert.NotNil(t, proposal)

	mBlockBuilder.AssertExpectations(t)
}

func TestFSM_verifyDistributeEmissionTx(t *testing.T) {
	activateAetherionEmissionForkForTest(t, 0)

	f := &fsm{
		isEndOfEpoch: true,
		epochNumber:  3,
		parent:       &types.Header{Number: 1},
	}

	// valid tx for an epoch-ending block, fork active
	tx, err := f.createDistributeEmissionTx()
	require.NoError(t, err)
	require.NoError(t, f.verifyDistributeEmissionTx(tx))

	// tampered tx (different epoch encoded) must fail the hash comparison
	otherEpoch := &fsm{isEndOfEpoch: true, epochNumber: 4, parent: &types.Header{Number: 1}}
	tampered, err := otherEpoch.createDistributeEmissionTx()
	require.NoError(t, err)
	assert.ErrorContains(t, f.verifyDistributeEmissionTx(tampered), "invalid distribute emission transaction")

	// not an epoch-ending block
	f.isEndOfEpoch = false
	assert.ErrorIs(t, f.verifyDistributeEmissionTx(tx), errDistributeEmissionTxNotExpected)
	f.isEndOfEpoch = true
}

func TestFSM_verifyDistributeEmissionTx_RejectsBeforeForkActive(t *testing.T) {
	// Fork left at its package default (disabled).
	fsm := &fsm{isEndOfEpoch: true, epochNumber: 3, parent: &types.Header{Number: 1}}

	tx := createStateTransactionWithData(fsm.Height(), types.StringToAddress("0xdead"), []byte{0x55, 0x35, 0xfe, 0x7b})
	assert.ErrorIs(t, fsm.verifyDistributeEmissionTx(tx), errDistributeEmissionTxForkNotActive)
}

func TestFSM_VerifyStateTransactions_EmissionForkActive_AllThreePass(t *testing.T) {
	activateAetherionEmissionForkForTest(t, 0)

	validators := validator.NewTestValidators(t, 5)
	validatorSet := validator.NewValidatorSet(validators.GetPublicIdentities(), hclog.NewNullLogger())

	fsm := &fsm{
		parent:                 &types.Header{Number: 1},
		isEndOfEpoch:           true,
		isEndOfSprint:          true,
		epochNumber:            0,
		validators:             validatorSet,
		commitEpochInput:       createTestCommitEpochInput(t, 0, 10),
		distributeRewardsInput: createTestDistributeRewardsInput(t, 0, validators.GetPublicIdentities(), 10),
		logger:                 hclog.NewNullLogger(),
	}

	commitEpochTx, err := fsm.createCommitEpochTx()
	require.NoError(t, err)

	distributeRewardsTx, err := fsm.createDistributeRewardsTx()
	require.NoError(t, err)

	distributeEmissionTx, err := fsm.createDistributeEmissionTx()
	require.NoError(t, err)

	require.NoError(t, fsm.VerifyStateTransactions(
		[]*types.Transaction{commitEpochTx, distributeRewardsTx, distributeEmissionTx}))
}

func TestFSM_VerifyStateTransactions_EmissionForkActive_MissingTxRejected(t *testing.T) {
	activateAetherionEmissionForkForTest(t, 0)

	validators := validator.NewTestValidators(t, 5)
	validatorSet := validator.NewValidatorSet(validators.GetPublicIdentities(), hclog.NewNullLogger())

	fsm := &fsm{
		parent:                 &types.Header{Number: 1},
		isEndOfEpoch:           true,
		isEndOfSprint:          true,
		epochNumber:            0,
		validators:             validatorSet,
		commitEpochInput:       createTestCommitEpochInput(t, 0, 10),
		distributeRewardsInput: createTestDistributeRewardsInput(t, 0, validators.GetPublicIdentities(), 10),
		logger:                 hclog.NewNullLogger(),
	}

	commitEpochTx, err := fsm.createCommitEpochTx()
	require.NoError(t, err)

	distributeRewardsTx, err := fsm.createDistributeRewardsTx()
	require.NoError(t, err)

	// Emission tx omitted even though the fork is active for this epoch -> must be
	// rejected, mirroring the existing commit-epoch/distribute-rewards requirement.
	assert.ErrorIs(t,
		fsm.VerifyStateTransactions([]*types.Transaction{commitEpochTx, distributeRewardsTx}),
		errDistributeEmissionTxDoesNotExist)
}

func TestFSM_VerifyStateTransactions_EmissionForkInactive_TxRejected(t *testing.T) {
	// Fork left at its package default (disabled): a proposer including the emission
	// tx anyway (e.g. a stale/malicious build) must be rejected, not silently accepted.
	fsm := &fsm{
		parent:           &types.Header{Number: 1},
		isEndOfEpoch:     true,
		commitEpochInput: createTestCommitEpochInput(t, 0, 10),
	}

	forgedInput, err := (&contractsapi.DistributeEmissionFn{EpochID: big.NewInt(0)}).EncodeAbi()
	require.NoError(t, err)
	forgedTx := createStateTransactionWithData(fsm.Height(), types.StringToAddress("0xdead"), forgedInput)

	commitEpochTx, err := fsm.createCommitEpochTx()
	require.NoError(t, err)

	err = fsm.VerifyStateTransactions([]*types.Transaction{commitEpochTx, forgedTx})
	assert.ErrorContains(t, err, errDistributeEmissionTxForkNotActive.Error())
}

func TestFSM_VerifyStateTransactions_EmissionForkActive_DuplicateTxRejected(t *testing.T) {
	activateAetherionEmissionForkForTest(t, 0)

	fsm := &fsm{
		parent:                 &types.Header{Number: 1},
		isEndOfEpoch:           true,
		epochNumber:            0,
		commitEpochInput:       createTestCommitEpochInput(t, 0, 10),
		distributeRewardsInput: createTestDistributeRewardsInput(t, 0, nil, 10),
	}

	commitEpochTx, err := fsm.createCommitEpochTx()
	require.NoError(t, err)

	distributeRewardsTx, err := fsm.createDistributeRewardsTx()
	require.NoError(t, err)

	distributeEmissionTx, err := fsm.createDistributeEmissionTx()
	require.NoError(t, err)

	err = fsm.VerifyStateTransactions(
		[]*types.Transaction{commitEpochTx, distributeRewardsTx, distributeEmissionTx, distributeEmissionTx})
	assert.ErrorIs(t, err, errDistributeEmissionTxSingleExpected)
}

// TestFSM_EmissionFork_ResyncDeterminism proves that, for a fixed binary (fixed
// AetherionEmissionForkEpoch / AetherionEmissionDistributorContract), building the
// distribute-emission tx for the same epoch always yields byte-identical output —
// the property a node resyncing from genesis depends on to reach the same state
// root as the rest of the network.
func TestFSM_EmissionFork_ResyncDeterminism(t *testing.T) {
	activateAetherionEmissionForkForTest(t, 5)

	build := func(epoch uint64) *types.Transaction {
		fsm := &fsm{parent: &types.Header{Number: 100}, epochNumber: epoch}
		tx, err := fsm.createDistributeEmissionTx()
		require.NoError(t, err)

		return tx
	}

	first := build(5)
	second := build(5)
	require.Equal(t, first.Hash, second.Hash)

	// Different epochs must not collide.
	different := build(6)
	require.NotEqual(t, first.Hash, different.Hash)
}

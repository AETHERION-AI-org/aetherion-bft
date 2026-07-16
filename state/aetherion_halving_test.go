package state

import (
	"math/big"
	"testing"

	"github.com/0xPolygon/polygon-edge/contracts"
	"github.com/0xPolygon/polygon-edge/types"
	"github.com/stretchr/testify/require"
)

// TestRawAetherionEpochReward verifies the pure halving ladder (50 → 25 → 12.5 → … → 0),
// independent of the emission fork. This is the raw per-epoch emission budget; the
// emission-fork zeroing applied by calculateAetherionEpochReward is covered separately in
// TestCalculateAetherionEpochReward_ForkGating.
func TestRawAetherionEpochReward(t *testing.T) {
	t.Parallel()

	require.Equal(t, "50000000000000000000", RawAetherionEpochReward(0).String())
	require.Equal(t, "50000000000000000000", RawAetherionEpochReward(aetherionHalvingIntervalEpochs-1).String())
	require.Equal(t, "25000000000000000000", RawAetherionEpochReward(aetherionHalvingIntervalEpochs).String())
	require.Equal(t, "12500000000000000000", RawAetherionEpochReward(aetherionHalvingIntervalEpochs*2).String())
	require.Equal(t, "0", RawAetherionEpochReward(aetherionHalvingIntervalEpochs*256).String())
}

// TestCalculateAetherionEpochReward_ForkGating is intentionally not t.Parallel(): it
// mutates the package-level contracts.AetherionEmissionForkEpoch /
// AetherionEmissionDistributorContract vars, shared with every other test in this
// package. That is safe against the t.Parallel() tests above and below because Go
// only resumes a parallel test's body (the part after t.Parallel()) once every
// non-parallel top-level test, including this one, has finished running — see
// https://pkg.go.dev/testing#T.Parallel.
func TestCalculateAetherionEpochReward_ForkGating(t *testing.T) {
	origEpoch := contracts.AetherionEmissionForkEpoch
	origAddr := contracts.AetherionEmissionDistributorContract

	defer func() {
		contracts.AetherionEmissionForkEpoch = origEpoch
		contracts.AetherionEmissionDistributorContract = origAddr
	}()

	forkEpoch := aetherionHalvingIntervalEpochs * 3
	contracts.AetherionEmissionForkEpoch = forkEpoch
	contracts.AetherionEmissionDistributorContract = types.StringToAddress("0xEEEE")

	// Pre-fork: halving math is completely unaffected by the fork existing.
	require.Equal(t, "50000000000000000000", calculateAetherionEpochReward(0).String())
	require.Equal(t, "50000000000000000000", calculateAetherionEpochReward(aetherionHalvingIntervalEpochs-1).String())
	require.Equal(t, "25000000000000000000", calculateAetherionEpochReward(aetherionHalvingIntervalEpochs).String())
	// One epoch before the fork boundary: still ordinary (pre-fork) halving math —
	// forkEpoch = 3 * interval, so this sits in the 2-halvings bracket (12.5e18),
	// not the fork's forced zero.
	require.Equal(t, "12500000000000000000", calculateAetherionEpochReward(forkEpoch-1).String())

	// At and after the fork epoch: legacy reward permanently zero, regardless of what
	// the halving schedule alone would otherwise have produced (would be 6.25e18 at
	// forkEpoch under the old math — the fork must override that, not coexist with it).
	require.Equal(t, "0", calculateAetherionEpochReward(forkEpoch).String())
	require.Equal(t, "0", calculateAetherionEpochReward(forkEpoch+1).String())
	require.Equal(t, "0", calculateAetherionEpochReward(aetherionHalvingIntervalEpochs*10).String())
}

// TestCalculateAetherionEpochReward_ForkInactiveWithoutDistributorAddress locks in the
// fail-safe behavior: reaching the fork epoch alone must never zero the legacy reward
// if the distributor address was never configured (half-finished build safety net).
func TestCalculateAetherionEpochReward_ForkInactiveWithoutDistributorAddress(t *testing.T) {
	origEpoch := contracts.AetherionEmissionForkEpoch
	origAddr := contracts.AetherionEmissionDistributorContract

	defer func() {
		contracts.AetherionEmissionForkEpoch = origEpoch
		contracts.AetherionEmissionDistributorContract = origAddr
	}()

	contracts.AetherionEmissionForkEpoch = 5
	// Explicitly clear the distributor address (main now carries the live baked prod
	// address) to exercise the half-configured fail-safe.
	contracts.AetherionEmissionDistributorContract = types.ZeroAddress

	// Fork epoch reached, but fails safe (no distributor configured): normal halving
	// math keeps running completely undisturbed, at epoch 5 and far beyond it.
	require.Equal(t, "50000000000000000000", calculateAetherionEpochReward(5).String())
	require.Equal(t, "25000000000000000000", calculateAetherionEpochReward(aetherionHalvingIntervalEpochs).String())
}

func TestAetherionRewardDistributionTxDetection(t *testing.T) {
	t.Parallel()

	input := make([]byte, 36)
	copy(input[:4], []byte(aetherionDistributeRewardForSelector))
	copy(input[4:36], types.BytesToHash(new(big.Int).SetUint64(42).Bytes()).Bytes())

	tx := &types.Transaction{
		Type:  types.StateTx,
		To:    contracts.RewardPoolContract.Ptr(),
		Input: input,
	}

	epochID, ok := decodeAetherionRewardEpoch(input)
	require.True(t, ok)
	require.Equal(t, uint64(42), epochID)
	require.True(t, isAetherionRewardDistributionTx(tx))
}

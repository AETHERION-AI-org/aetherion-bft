package contracts

import (
	"math"
	"testing"

	"github.com/0xPolygon/polygon-edge/types"
	"github.com/stretchr/testify/require"
)

// The build carries the router fork either fully disabled (zero address, MaxUint64) or fully
// baked (a real proxy and a real epoch). Anything in between is a release mistake: an epoch
// without an address never activates, an address without an epoch reads as "configured" to a
// reviewer while doing nothing. Asserting the pair instead of the disabled defaults lets the
// activation commit bake the values without rewriting this test, and still fails a half-bake.
func TestRewardsRouterFork_BuildIsFullyDisabledOrFullyBaked(t *testing.T) {
	disabled := AetherionRewardsRouterContract == types.ZeroAddress
	require.Equal(t, disabled, AetherionRewardsRouterForkEpoch == math.MaxUint64,
		"router address %s and fork epoch %d must be set together", AetherionRewardsRouterContract, AetherionRewardsRouterForkEpoch)

	if disabled {
		require.False(t, IsAetherionRewardsRouterActive(0))
		require.False(t, IsAetherionRewardsRouterActive(math.MaxUint64))

		return
	}

	require.Positive(t, AetherionRewardsRouterForkEpoch)
	require.False(t, IsAetherionRewardsRouterActive(AetherionRewardsRouterForkEpoch-1))
	require.True(t, IsAetherionRewardsRouterActive(AetherionRewardsRouterForkEpoch))
}

// The disabled state has to keep working whatever the build carries — it is what a rollback
// restores.
func TestRewardsRouterFork_DisabledStateStaysInert(t *testing.T) {
	origAddr := AetherionRewardsRouterContract
	origEpoch := AetherionRewardsRouterForkEpoch

	t.Cleanup(func() {
		AetherionRewardsRouterContract = origAddr
		AetherionRewardsRouterForkEpoch = origEpoch
	})

	AetherionRewardsRouterContract = types.ZeroAddress
	AetherionRewardsRouterForkEpoch = math.MaxUint64

	require.False(t, IsAetherionRewardsRouterActive(0))
	require.False(t, IsAetherionRewardsRouterActive(math.MaxUint64))
}

func TestRewardsRouterFork_GatingLogic(t *testing.T) {
	origAddr := AetherionRewardsRouterContract
	origEpoch := AetherionRewardsRouterForkEpoch

	t.Cleanup(func() {
		AetherionRewardsRouterContract = origAddr
		AetherionRewardsRouterForkEpoch = origEpoch
	})

	AetherionRewardsRouterForkEpoch = 100
	AetherionRewardsRouterContract = types.StringToAddress("0xabc")

	require.False(t, IsAetherionRewardsRouterActive(99))
	require.True(t, IsAetherionRewardsRouterActive(100))
	require.True(t, IsAetherionRewardsRouterActive(101))
	require.True(t, IsAetherionRewardsRouterActive(math.MaxUint64))

	// Half-configured (fork epoch set, address forgotten) must stay inactive rather than
	// aim a per-epoch system transaction at the zero address.
	AetherionRewardsRouterContract = types.ZeroAddress
	require.False(t, IsAetherionRewardsRouterActive(100))
}

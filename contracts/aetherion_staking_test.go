package contracts

import (
	"math"
	"testing"

	"github.com/0xPolygon/polygon-edge/types"
	"github.com/stretchr/testify/require"
)

// The staking fork must default to fully disabled so a plain build behaves exactly like
// the pre-fork node: frozen validator set, no reward transaction.
func TestStakingFork_DisabledByDefault(t *testing.T) {
	require.Equal(t, types.ZeroAddress, AetherionValidatorRegistryContract)
	require.Equal(t, types.ZeroAddress, AetherionValidatorRewardsContract)
	require.Equal(t, uint64(math.MaxUint64), AetherionStakingForkEpoch)

	require.False(t, IsAetherionStakingForkConfigured())
	require.False(t, IsAetherionStakingForkActive(0))
	require.False(t, IsAetherionStakingForkActive(math.MaxUint64))
	require.False(t, IsAetherionValidatorRewardsActive(math.MaxUint64))
}

func TestStakingFork_GatingLogic(t *testing.T) {
	origRegistry := AetherionValidatorRegistryContract
	origRewards := AetherionValidatorRewardsContract
	origEpoch := AetherionStakingForkEpoch

	t.Cleanup(func() {
		AetherionValidatorRegistryContract = origRegistry
		AetherionValidatorRewardsContract = origRewards
		AetherionStakingForkEpoch = origEpoch
	})

	AetherionValidatorRegistryContract = types.StringToAddress("0xabc")
	AetherionStakingForkEpoch = 100

	require.True(t, IsAetherionStakingForkConfigured())

	// fork gated by epoch
	require.False(t, IsAetherionStakingForkActive(99))
	require.True(t, IsAetherionStakingForkActive(100))
	require.True(t, IsAetherionStakingForkActive(101))

	// rewards require the rewards address too — fail-safe: never target the zero address
	require.False(t, IsAetherionValidatorRewardsActive(100))
	AetherionValidatorRewardsContract = types.StringToAddress("0xdef")
	require.True(t, IsAetherionValidatorRewardsActive(100))
	require.False(t, IsAetherionValidatorRewardsActive(99))

	// half-configured (fork epoch set, registry forgotten) → inactive
	AetherionValidatorRegistryContract = types.ZeroAddress
	require.False(t, IsAetherionStakingForkActive(100))
	require.False(t, IsAetherionValidatorRewardsActive(100))
}

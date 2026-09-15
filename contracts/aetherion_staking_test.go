package contracts

import (
	"math"
	"testing"

	"github.com/0xPolygon/polygon-edge/types"
	"github.com/stretchr/testify/require"
)

// The staking fork is permanently active on chain 100892 from epoch 12307 (activated
// 2026-07-15); main carries the baked activation values so any build reproduces the live
// chain. Not parallel: asserts the package-level values directly, which the gating test
// below temporarily overrides and restores.
//
// This test used to assert the "disabled by default" state. Baking the live values is what
// flips it, and updating it is part of the activation commit — the same edit the emission
// fork's test already carries.
func TestStakingFork_LiveOnMainnet(t *testing.T) {
	require.Equal(t,
		types.StringToAddress("0x6ebA8468F754404C1c93ae94C2D1973683eb749A"),
		AetherionValidatorRegistryContract)
	require.Equal(t,
		types.StringToAddress("0x869fEa83CC84d1F1B485ce993839779B6A1e4fc6"),
		AetherionValidatorRewardsContract)
	require.Equal(t, uint64(12307), AetherionStakingForkEpoch)

	require.True(t, IsAetherionStakingForkConfigured())
	require.False(t, IsAetherionStakingForkActive(12306))
	require.True(t, IsAetherionStakingForkActive(12307))
	require.True(t, IsAetherionValidatorRewardsActive(12307))
	require.False(t, IsAetherionValidatorRewardsActive(12306))
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
	AetherionValidatorRewardsContract = types.ZeroAddress
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

// The disabled state still has to work — it is what every build looks like before a fork is
// baked, and what a rollback restores. Asserted by override rather than by the package
// defaults, which now carry the live values.
func TestStakingFork_DisabledStateStaysInert(t *testing.T) {
	origRegistry := AetherionValidatorRegistryContract
	origRewards := AetherionValidatorRewardsContract
	origEpoch := AetherionStakingForkEpoch

	t.Cleanup(func() {
		AetherionValidatorRegistryContract = origRegistry
		AetherionValidatorRewardsContract = origRewards
		AetherionStakingForkEpoch = origEpoch
	})

	AetherionValidatorRegistryContract = types.ZeroAddress
	AetherionValidatorRewardsContract = types.ZeroAddress
	AetherionStakingForkEpoch = math.MaxUint64

	require.False(t, IsAetherionStakingForkConfigured())
	require.False(t, IsAetherionStakingForkActive(0))
	require.False(t, IsAetherionStakingForkActive(math.MaxUint64))
	require.False(t, IsAetherionValidatorRewardsActive(math.MaxUint64))
}

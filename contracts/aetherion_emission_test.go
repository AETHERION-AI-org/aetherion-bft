package contracts

import (
	"math"
	"testing"

	"github.com/0xPolygon/polygon-edge/types"
	"github.com/stretchr/testify/require"
)

func TestIsAetherionEmissionForkActive_LiveOnMainnet(t *testing.T) {
	// The emission fork is permanently active on chain 100892 from epoch 12262
	// (activated 2026-07-15); main carries the baked activation values so any build
	// reproduces the live chain. Not parallel: asserts the package-level values directly,
	// which other tests below temporarily override (and restore via defer).
	require.Equal(t,
		types.StringToAddress("0x347bB4E2eDb5135458F03488754905D511F38863"),
		AetherionEmissionDistributorContract)
	require.Equal(t, uint64(12262), AetherionEmissionForkEpoch)

	require.False(t, IsAetherionEmissionForkActive(12261))
	require.True(t, IsAetherionEmissionForkActive(12262))
	require.True(t, IsAetherionEmissionForkActive(1_000_000))
}

func TestIsAetherionEmissionForkActive_FailsSafeWithoutDistributorAddress(t *testing.T) {
	origEpoch := AetherionEmissionForkEpoch
	origAddr := AetherionEmissionDistributorContract

	defer func() {
		AetherionEmissionForkEpoch = origEpoch
		AetherionEmissionDistributorContract = origAddr
	}()

	AetherionEmissionForkEpoch = 100
	AetherionEmissionDistributorContract = types.ZeroAddress

	// Epoch threshold reached, but the distributor address is not configured: the fork
	// must stay inactive rather than zero the legacy reward with nothing to replace it.
	require.False(t, IsAetherionEmissionForkActive(100))
	require.False(t, IsAetherionEmissionForkActive(1_000_000))
}

func TestIsAetherionEmissionForkActive_ActivatesExactlyAtBoundary(t *testing.T) {
	origEpoch := AetherionEmissionForkEpoch
	origAddr := AetherionEmissionDistributorContract

	defer func() {
		AetherionEmissionForkEpoch = origEpoch
		AetherionEmissionDistributorContract = origAddr
	}()

	AetherionEmissionForkEpoch = 100
	AetherionEmissionDistributorContract = types.StringToAddress("0x1234")

	require.False(t, IsAetherionEmissionForkActive(99))
	require.True(t, IsAetherionEmissionForkActive(100))
	require.True(t, IsAetherionEmissionForkActive(101))
	require.True(t, IsAetherionEmissionForkActive(math.MaxUint64))
}

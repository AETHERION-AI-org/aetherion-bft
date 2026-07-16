package contractsapi

import (
	"encoding/hex"
	"math/big"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestDistributeValidatorRewardsFn_Selector locks in the 4-byte selector for
// distribute(uint256,uint256), independently computed as
// keccak256("distribute(uint256,uint256)")[:4] and verified against the compiled
// AetherionValidatorRewards ABI (ethers.id) in the contracts/ package. A change here
// means the Go side and the Solidity contract have drifted apart.
func TestDistributeValidatorRewardsFn_Selector(t *testing.T) {
	t.Parallel()

	fn := &DistributeValidatorRewardsFn{}
	require.Equal(t, "7625391a", hex.EncodeToString(fn.Sig()))
}

func TestDistributeValidatorRewardsFn_EncodeDecodeRoundTrip(t *testing.T) {
	t.Parallel()

	type tc struct {
		epoch  *big.Int
		amount *big.Int
	}

	cases := []tc{
		{big.NewInt(0), big.NewInt(0)},
		{big.NewInt(1), new(big.Int).Mul(big.NewInt(6), big.NewInt(1e18))},
		{big.NewInt(210_240), new(big.Int).Mul(big.NewInt(3), big.NewInt(1e18))},
		{new(big.Int).SetUint64(^uint64(0)), big.NewInt(123456789)},
	}

	for _, c := range cases {
		original := &DistributeValidatorRewardsFn{EpochID: c.epoch, Amount: c.amount}

		encoded, err := original.EncodeAbi()
		require.NoError(t, err)
		require.Len(t, encoded, 4+32+32) // selector + two uint256 words

		decoded := &DistributeValidatorRewardsFn{}
		require.NoError(t, decoded.DecodeAbi(encoded))
		require.Equal(t, 0, c.epoch.Cmp(decoded.EpochID), "epoch mismatch")
		require.Equal(t, 0, c.amount.Cmp(decoded.Amount), "amount mismatch")
	}
}

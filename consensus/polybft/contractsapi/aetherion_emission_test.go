package contractsapi

import (
	"encoding/hex"
	"math/big"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestDistributeEmissionFn_Selector locks in the 4-byte selector for
// distributeEpoch(uint256), independently computed as
// keccak256("distributeEpoch(uint256)")[:4] (verified against
// AetherionEmissionDistributor's compiled ABI via ethers.id in the contracts/ package).
// A change here means the Go side and the Solidity contract have drifted apart.
func TestDistributeEmissionFn_Selector(t *testing.T) {
	t.Parallel()

	fn := &DistributeEmissionFn{}
	require.Equal(t, "5535fe7b", hex.EncodeToString(fn.Sig()))
}

func TestDistributeEmissionFn_EncodeDecodeRoundTrip(t *testing.T) {
	t.Parallel()

	cases := []*big.Int{
		big.NewInt(0),
		big.NewInt(1),
		big.NewInt(210_240),
		new(big.Int).SetUint64(^uint64(0)), // math.MaxUint64
	}

	for _, epochID := range cases {
		original := &DistributeEmissionFn{EpochID: epochID}

		encoded, err := original.EncodeAbi()
		require.NoError(t, err)
		require.Len(t, encoded, 4+32) // selector + one uint256 word

		decoded := &DistributeEmissionFn{}
		require.NoError(t, decoded.DecodeAbi(encoded))
		// big.Int equality via Cmp, not require.Equal: a decoded zero value has a
		// non-nil-but-empty internal `abs` slice where big.NewInt(0) has a nil one —
		// semantically equal (Cmp reports 0) but reflect.DeepEqual would disagree.
		require.Equal(t, 0, epochID.Cmp(decoded.EpochID), "epochID=%s decoded=%s", epochID, decoded.EpochID)
	}
}

func TestDistributeEmissionFn_DecodeRejectsWrongSelector(t *testing.T) {
	t.Parallel()

	other := &CommitEpochValidatorSetFn{}
	otherEncoded, err := (&CommitEpochValidatorSetFn{
		ID:    big.NewInt(1),
		Epoch: &Epoch{StartBlock: big.NewInt(1), EndBlock: big.NewInt(2)},
	}).EncodeAbi()
	require.NoError(t, err)
	_ = other

	decoded := &DistributeEmissionFn{}
	require.Error(t, decoded.DecodeAbi(otherEncoded))
}

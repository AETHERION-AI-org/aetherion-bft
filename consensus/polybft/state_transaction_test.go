package polybft

import (
	"encoding/hex"
	"math/big"
	"reflect"
	"testing"

	"github.com/0xPolygon/polygon-edge/consensus/polybft/contractsapi"
	"github.com/0xPolygon/polygon-edge/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/umbracle/ethgo/abi"
)

func TestStateTransaction_Signature(t *testing.T) {
	t.Parallel()

	cases := []struct {
		m   *abi.Method
		sig string
	}{
		{
			contractsapi.ValidatorSet.Abi.GetMethod("commitEpoch"),
			"0f50287c",
		},
	}
	for _, c := range cases {
		sig := hex.EncodeToString(c.m.ID())
		require.Equal(t, c.sig, sig)
	}

	// DistributeEmissionFn is hand-declared (see contractsapi/aetherion_emission.go),
	// not backed by an abi.Method from a generated artifact, so it is asserted
	// separately rather than through the abi.Method-based table above.
	emissionFn := &contractsapi.DistributeEmissionFn{}
	require.Equal(t, "5535fe7b", hex.EncodeToString(emissionFn.Sig()))
}

func TestStateTransaction_Encoding(t *testing.T) {
	t.Parallel()

	cases := []contractsapi.StateTransactionInput{
		&contractsapi.CommitEpochValidatorSetFn{
			ID: big.NewInt(1),
			Epoch: &contractsapi.Epoch{
				StartBlock: big.NewInt(1),
				EndBlock:   big.NewInt(10),
				EpochRoot:  types.Hash{},
			},
		},
		&contractsapi.DistributeEmissionFn{
			EpochID: big.NewInt(210_240),
		},
	}

	for _, c := range cases {
		res, err := c.EncodeAbi()

		require.NoError(t, err)

		// use reflection to create another type and decode
		val := reflect.New(reflect.TypeOf(c).Elem()).Interface()
		obj, ok := val.(contractsapi.StateTransactionInput)
		assert.True(t, ok)

		err = obj.DecodeAbi(res)
		require.NoError(t, err)

		require.Equal(t, obj, c)
	}
}

func TestDecodeStateTransaction_DistributeEmission(t *testing.T) {
	t.Parallel()

	input, err := (&contractsapi.DistributeEmissionFn{EpochID: big.NewInt(210_240)}).EncodeAbi()
	require.NoError(t, err)

	decoded, err := decodeStateTransaction(input)
	require.NoError(t, err)

	emissionFn, ok := decoded.(*contractsapi.DistributeEmissionFn)
	require.True(t, ok)
	require.Equal(t, big.NewInt(210_240), emissionFn.EpochID)
}

func TestDecodeStateTransaction_UnknownSelectorRejected(t *testing.T) {
	t.Parallel()

	_, err := decodeStateTransaction([]byte{0xde, 0xad, 0xbe, 0xef})
	require.ErrorContains(t, err, "unknown state transaction")
}

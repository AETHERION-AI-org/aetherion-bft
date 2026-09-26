package state_test

import (
	"math/big"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/0xPolygon/polygon-edge/state/runtime/tracer/calltracer"
	"github.com/0xPolygon/polygon-edge/types"
)

func traceTopFrame(t *testing.T, msg *types.Transaction) *calltracer.Call {
	t.Helper()

	transition := newReplayTransition(t)
	tracer := &calltracer.CallTracer{}
	transition.SetTracer(tracer)

	require.NoError(t, transition.Write(msg))

	res, err := tracer.GetResult()
	require.NoError(t, err)

	frame, ok := res.(*calltracer.Call)
	require.True(t, ok, "Blockscout crashes on a null trace, so every tx needs a top frame")
	require.NotNil(t, frame)

	return frame
}

// Chain 100892 tx 0x41f8bb62… (block 6503627): a plain transfer the sender could not
// cover failed in the value transfer, before the frame opened, and the trace was null.
func TestTrace_FailedValueTransferHasAFrameWithTheError(t *testing.T) {
	t.Parallel()

	to := types.StringToAddress("0x4000000000000000000000000000000000000004")
	frame := traceTopFrame(t, &types.Transaction{
		From:     replaySender,
		To:       &to,
		Gas:      21_000,
		GasPrice: big.NewInt(0),
		Value:    big.NewInt(5), // sender holds 1
	})

	require.Equal(t, "CALL", frame.Type)
	require.Equal(t, "insufficient balance for transfer", frame.Error)
}

// Every deployment on chain 100892 traced as "UNKNOWN" with empty input: the executor
// passed the opcode (0xF0) where the tracer expects a call type.
func TestTrace_CreationIsACreateFrameWithInitCode(t *testing.T) {
	t.Parallel()

	frame := traceTopFrame(t, &types.Transaction{
		From:     replaySender,
		Gas:      100_000,
		GasPrice: big.NewInt(0),
		Value:    big.NewInt(0),
		Input:    []byte{0x00}, // STOP: deploys empty code
	})

	require.Equal(t, "CREATE", frame.Type)
	require.Equal(t, "0x00", frame.Input)
	require.Empty(t, frame.Error)
}

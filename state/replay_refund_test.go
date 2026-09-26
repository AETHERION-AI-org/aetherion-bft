package state_test

import (
	"math/big"
	"testing"

	"github.com/hashicorp/go-hclog"
	"github.com/stretchr/testify/require"

	"github.com/0xPolygon/polygon-edge/chain"
	"github.com/0xPolygon/polygon-edge/state"
	itrie "github.com/0xPolygon/polygon-edge/state/immutable-trie"
	"github.com/0xPolygon/polygon-edge/types"
)

var (
	replaySender  = types.StringToAddress("0x1000000000000000000000000000000000000001")
	replayClearer = types.StringToAddress("0x2000000000000000000000000000000000000002")
	replaySetter  = types.StringToAddress("0x3000000000000000000000000000000000000003")
)

// newReplayTransition starts a block with two contracts: replayClearer zeroes a set slot
// (earns an SSTORE refund), replaySetter writes an empty slot (earns none).
func newReplayTransition(t *testing.T) *state.Transition {
	t.Helper()

	ex := state.NewExecutor(&chain.Params{
		Forks:        chain.AllForksEnabled,
		BurnContract: map[uint64]types.Address{0: types.ZeroAddress},
	}, itrie.NewState(itrie.NewMemoryStorage()), hclog.NewNullLogger())

	root, err := ex.WriteGenesis(map[types.Address]*chain.GenesisAccount{
		replaySender: {Balance: big.NewInt(1)},
		// PUSH1 0 PUSH1 0 SSTORE STOP
		replayClearer: {
			Code:    []byte{0x60, 0x00, 0x60, 0x00, 0x55, 0x00},
			Storage: map[types.Hash]types.Hash{{}: types.BytesToHash([]byte{1})},
		},
		// PUSH1 1 PUSH1 0 SSTORE STOP
		replaySetter: {Code: []byte{0x60, 0x01, 0x60, 0x00, 0x55, 0x00}},
	}, types.Hash{})
	require.NoError(t, err)

	ex.GetHash = func(*types.Header) state.GetHashByNumber {
		return func(uint64) types.Hash { return root }
	}

	transition, err := ex.BeginTxn(root, &types.Header{Number: 1, GasLimit: 10_000_000}, types.ZeroAddress)
	require.NoError(t, err)

	return transition
}

func replayCall(nonce uint64, to types.Address) *types.Transaction {
	return &types.Transaction{
		Nonce:    nonce,
		From:     replaySender,
		To:       &to,
		Gas:      100_000,
		GasPrice: big.NewInt(0),
		Value:    big.NewInt(0),
	}
}

// debug_traceTransaction replays the txs before the target. It must replay them the way
// block import does (Write), or the refund counter of one tx leaks into the next and the
// replay's gas drifts from the block's: on chain 100892 that drift ended in "gas limit
// reached in the pool" for block 6683351 and Blockscout lost the block's internal txs.
func TestReplay_WriteResetsRefundBetweenTxs(t *testing.T) {
	t.Parallel()

	standalone := newReplayTransition(t)
	single, err := standalone.Apply(replayCall(0, replaySetter))
	require.NoError(t, err)

	imported := newReplayTransition(t)
	require.NoError(t, imported.Write(replayCall(0, replayClearer)))
	require.NoError(t, imported.Write(replayCall(1, replaySetter)))
	require.Equal(t, single.GasUsed, imported.Receipts()[1].GasUsed,
		"a tx after a refunding tx must cost what it costs alone")

	// The bug the trace replay had: bare Apply keeps the first tx's refund.
	leaky := newReplayTransition(t)
	_, err = leaky.Apply(replayCall(0, replayClearer))
	require.NoError(t, err)
	second, err := leaky.Apply(replayCall(1, replaySetter))
	require.NoError(t, err)
	require.Less(t, second.GasUsed, single.GasUsed)
}

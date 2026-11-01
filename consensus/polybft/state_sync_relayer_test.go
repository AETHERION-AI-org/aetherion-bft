package polybft

import (
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/0xPolygon/polygon-edge/consensus/polybft/contractsapi"
	"github.com/0xPolygon/polygon-edge/types"
	"github.com/hashicorp/go-hclog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/umbracle/ethgo"
	"github.com/umbracle/ethgo/abi"
)

func TestStateSyncRelayer_FullWorkflow(t *testing.T) {
	t.Parallel()

	testKey := createTestKey(t)
	stateSyncAddr := types.StringToAddress("0x56563")

	resultLogs := []*types.Log{
		createTestLogForStateSyncResultEvent(t, 1), createTestLogForStateSyncResultEvent(t, 2),
		createTestLogForStateSyncResultEvent(t, 3), createTestLogForStateSyncResultEvent(t, 4),
		createTestLogForStateSyncResultEvent(t, 5),
	}
	commitmentLogs := []*types.Log{
		createTestLogForNewCommitmentEvent(t, stateSyncAddr, 1, 1, types.StringToHash("0x2")),
		createTestLogForNewCommitmentEvent(t, stateSyncAddr, 2, 3, types.StringToHash("0x2")),
		createTestLogForNewCommitmentEvent(t, stateSyncAddr, 4, 5, types.StringToHash("0x2")),
	}

	headers := []*types.Header{
		{Number: 2}, {Number: 3}, {Number: 4}, {Number: 5}, {Number: 5},
	}

	proofMock := &mockStateSyncProofRetriever{
		fn: func(stateSyncID uint64) (types.Proof, error) {
			return types.Proof{
				Data: []types.Hash{types.StringToHash("0x1122334455")},
				Metadata: map[string]interface{}{
					"StateSync": map[string]interface{}{
						"ID":       stateSyncID,
						"Sender":   types.StringToAddress("0xffee"),
						"Receiver": types.StringToAddress("0xeeff"),
						"Data":     nil,
					},
				},
			}, nil
		},
	}
	blockhainMock := &blockchainMock{}
	dummyTxRelayer := newDummyStakeTxRelayer(t, nil)
	state := newTestState(t)

	stateSyncRelayer := NewStateSyncRelayer(
		dummyTxRelayer,
		stateSyncAddr,
		state.StateSyncStore,
		proofMock,
		blockhainMock,
		testKey,
		&stateSyncRelayerConfig{
			maxAttemptsToSend:        6,
			maxBlocksToWaitForResend: 1,
			maxEventsPerBatch:        1,
		},
		hclog.Default(),
	)

	for _, h := range headers {
		blockhainMock.On("CurrentHeader").Return(h).Once()
	}

	// send first two events without errors
	dummyTxRelayer.On("SendTransaction", mock.Anything, testKey).Return((*ethgo.Receipt)(nil), nil).Times(2)
	// fail 3rd time
	dummyTxRelayer.On("SendTransaction", mock.Anything, testKey).Return(
		(*ethgo.Receipt)(nil), errors.New("e")).Once()
	// send 3 events all at once at the end
	dummyTxRelayer.On("SendTransaction", mock.Anything, testKey).Return((*ethgo.Receipt)(nil), nil).Once()

	require.NoError(t, stateSyncRelayer.Init())

	// The relayer processes events in its own goroutine (PostBlock only notifies it), so
	// poll for each expected state instead of sleeping a fixed 2s: on a loaded machine one
	// processEvents round (bolt write + SendTransaction) can outlast the sleep, and the test
	// failed intermittently. Every state below is reached only after the test's own
	// ProcessLog/PostBlock call, so polling cannot skip past an intermediate state.
	const (
		waitFor = 30 * time.Second
		tick    = 50 * time.Millisecond
	)

	// post 1st block
	require.NoError(t, stateSyncRelayer.ProcessLog(headers[0], convertLog(commitmentLogs[0]), nil))
	require.NoError(t, stateSyncRelayer.ProcessLog(headers[0], convertLog(commitmentLogs[1]), nil))
	require.NoError(t, stateSyncRelayer.PostBlock(&PostBlockRequest{}))

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		events, err := state.StateSyncStore.getAllAvailableEvents(0)
		if !assert.NoError(c, err) || !assert.Len(c, events, 3) {
			return
		}

		assert.Equal(c, uint64(1), events[0].EventID)
		assert.True(c, events[0].SentStatus)
		assert.False(c, events[1].SentStatus)
		assert.False(c, events[2].SentStatus)
	}, waitFor, tick)

	// post 2nd block
	require.NoError(t, stateSyncRelayer.ProcessLog(headers[1], convertLog(resultLogs[0]), nil))
	require.NoError(t, stateSyncRelayer.ProcessLog(headers[1], convertLog(commitmentLogs[2]), nil))
	require.NoError(t, stateSyncRelayer.PostBlock(&PostBlockRequest{}))

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		events, err := state.StateSyncStore.getAllAvailableEvents(0)
		if !assert.NoError(c, err) || !assert.Len(c, events, 4) {
			return
		}

		assert.True(c, events[0].SentStatus)
		assert.Equal(c, uint64(2), events[0].EventID)
		assert.False(c, events[1].SentStatus)
		assert.False(c, events[2].SentStatus)
	}, waitFor, tick)

	// post 3rd block
	require.NoError(t, stateSyncRelayer.ProcessLog(headers[2], convertLog(resultLogs[1]), nil))
	require.NoError(t, stateSyncRelayer.PostBlock(&PostBlockRequest{}))

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		events, err := state.StateSyncStore.getAllAvailableEvents(0)
		if !assert.NoError(c, err) || !assert.Len(c, events, 3) {
			return
		}

		assert.True(c, events[0].SentStatus)
		assert.Equal(c, uint64(3), events[0].EventID)
		assert.False(c, events[1].SentStatus)
	}, waitFor, tick)

	// post 4th block - will not provide result, so one more SendTransaction will be triggered
	stateSyncRelayer.config.maxEventsPerBatch = 3 // send all 3 left events at once

	require.NoError(t, stateSyncRelayer.PostBlock(&PostBlockRequest{}))

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		events, err := state.StateSyncStore.getAllAvailableEvents(0)
		if !assert.NoError(c, err) || !assert.Len(c, events, 3) {
			return
		}

		assert.True(c, events[0].SentStatus && events[1].SentStatus && events[2].SentStatus)
	}, waitFor, tick)

	// post 5th block
	require.NoError(t, stateSyncRelayer.ProcessLog(headers[4], convertLog(resultLogs[2]), nil))
	require.NoError(t, stateSyncRelayer.ProcessLog(headers[4], convertLog(resultLogs[3]), nil))
	require.NoError(t, stateSyncRelayer.ProcessLog(headers[4], convertLog(resultLogs[4]), nil))
	require.NoError(t, stateSyncRelayer.PostBlock(&PostBlockRequest{}))

	events, err := state.StateSyncStore.getAllAvailableEvents(0)

	require.NoError(t, err)
	require.Len(t, events, 0)

	// wait until the relayer goroutine has made every expected call (5 CurrentHeader,
	// 4 SendTransaction) before closing it, instead of sleeping and hoping it is done
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		blockhainMock.AssertExpectations(collectLogT{c})
		dummyTxRelayer.AssertExpectations(collectLogT{c})
	}, waitFor, tick)

	stateSyncRelayer.Close()
}

// collectLogT adapts assert.CollectT to mock.TestingT (which also requires Logf),
// so mock expectations can be polled with require.EventuallyWithT.
type collectLogT struct{ *assert.CollectT }

func (collectLogT) Logf(string, ...interface{}) {}

type mockStateSyncProofRetriever struct {
	fn func(uint64) (types.Proof, error)
}

func (m *mockStateSyncProofRetriever) GetStateSyncProof(stateSyncID uint64) (types.Proof, error) {
	if m.fn != nil {
		return m.fn(stateSyncID)
	}

	return types.Proof{}, nil
}

func createTestLogForNewCommitmentEvent(
	t *testing.T,
	stateSyncAddr types.Address,
	startEventID uint64,
	endEventID uint64,
	rootHash types.Hash) *types.Log {
	t.Helper()

	var evnt contractsapi.NewCommitmentEvent

	encodedData1, err := abi.MustNewType("uint256").Encode(new(big.Int).SetUint64(startEventID))
	require.NoError(t, err)

	encodedData2, err := abi.MustNewType("uint256").Encode(new(big.Int).SetUint64(endEventID))
	require.NoError(t, err)

	return &types.Log{
		Address: stateSyncAddr,
		Topics: []types.Hash{
			types.Hash(evnt.Sig()),
			types.BytesToHash(encodedData1),
			types.BytesToHash(encodedData2),
		},
		Data: rootHash[:],
	}
}

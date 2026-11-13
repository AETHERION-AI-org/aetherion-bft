package polybft

import (
	"testing"
	"time"

	"github.com/0xPolygon/polygon-edge/blockchain"
	"github.com/0xPolygon/polygon-edge/types"
	"github.com/hashicorp/go-hclog"
	"github.com/stretchr/testify/require"
)

func syncerEvent(number uint64) *blockchain.Event {
	return &blockchain.Event{Source: "syncer", NewChain: []*types.Header{{Number: number}}}
}

// The consensus loop busy in stopSequence must not stop the subscription from being drained: a full
// subscription blocks WriteBlock under the blockchain's write lock, which the sequence being stopped
// may be waiting for (13.11 review).
func TestForwardSyncerBlocks_KeepsDrainingWhileTheLoopIsBusy(t *testing.T) {
	closeCh := make(chan struct{})
	defer close(closeCh)

	eventCh := make(chan *blockchain.Event, 5) // the subscription's buffer
	syncerBlockCh := make(chan struct{}, 1)    // nobody reads it during the test

	go forwardSyncerBlocks(closeCh, eventCh, func() uint64 { return 1 }, syncerBlockCh, hclog.NewNullLogger())

	pushed := make(chan struct{})

	go func() {
		for n := uint64(1); n <= 50; n++ {
			eventCh <- syncerEvent(n)
		}

		close(pushed)
	}()

	select {
	case <-pushed:
	case <-time.After(2 * time.Second):
		t.Fatal("the forwarder stopped draining the subscription while the loop was busy")
	}

	require.Len(t, syncerBlockCh, 1, "signals coalesce into one")
}

func TestForwardSyncerBlocks_IgnoresStaleAndNonSyncerEvents(t *testing.T) {
	closeCh := make(chan struct{})
	defer close(closeCh)

	eventCh := make(chan *blockchain.Event, 5)
	syncerBlockCh := make(chan struct{}, 1)

	go forwardSyncerBlocks(closeCh, eventCh, func() uint64 { return 10 }, syncerBlockCh, hclog.NewNullLogger())

	eventCh <- syncerEvent(9)
	eventCh <- &blockchain.Event{Source: "consensus", NewChain: []*types.Header{{Number: 11}}}
	eventCh <- &blockchain.Event{Source: "syncer"}
	eventCh <- nil

	require.Never(t, func() bool { return len(syncerBlockCh) > 0 }, 200*time.Millisecond, 10*time.Millisecond)

	eventCh <- syncerEvent(10)

	require.Eventually(t, func() bool { return len(syncerBlockCh) == 1 }, time.Second, 10*time.Millisecond)
}

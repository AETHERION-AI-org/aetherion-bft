package polybft

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/hashicorp/go-hclog"
	"github.com/stretchr/testify/require"
)

func runWatch(t *testing.T, running *atomic.Uint64, head *atomic.Uint64) (<-chan uint64, func()) {
	t.Helper()

	closeCh := make(chan struct{})
	cancelCh := make(chan uint64, 1)

	go watchOvertakenSequence(closeCh, running, head.Load, cancelCh, 50*time.Millisecond, hclog.NewNullLogger())

	return cancelCh, func() { close(closeCh) }
}

func TestWatchOvertakenSequence_CancelsASequenceWhoseHeightIsOnChain(t *testing.T) {
	t.Parallel()

	var running, head atomic.Uint64

	running.Store(8132285)
	head.Store(8132285)

	cancelCh, stop := runWatch(t, &running, &head)
	defer stop()

	select {
	case height := <-cancelCh:
		require.Equal(t, uint64(8132285), height)
	case <-time.After(2 * time.Second):
		require.Fail(t, "stuck sequence was not canceled")
	}
}

// A main loop busy in stopSequence must not hold the watcher, or its signal lands on the next height.
func TestWatchOvertakenSequence_NeverWaitsForTheMainLoop(t *testing.T) {
	t.Parallel()

	var running, looks atomic.Uint64

	running.Store(10)

	closeCh := make(chan struct{})
	defer close(closeCh)

	cancelCh := make(chan uint64, 1)
	head := func() uint64 {
		looks.Add(1)

		return 10
	}

	go watchOvertakenSequence(closeCh, &running, head, cancelCh, 20*time.Millisecond, hclog.NewNullLogger())

	time.Sleep(200 * time.Millisecond) // the buffer filled long ago, nobody reads it

	before := looks.Load()

	require.Eventually(t, func() bool { return looks.Load() > before+5 }, 2*time.Second, 5*time.Millisecond,
		"watcher stopped looking while the signal waited to be read")
}

func TestOvertakenIsCurrent(t *testing.T) {
	t.Parallel()

	require.True(t, overtakenIsCurrent(11, 10))
	require.False(t, overtakenIsCurrent(10, 10), "signal for the height before the running one is stale")
	require.False(t, overtakenIsCurrent(9, 10))
}

func TestWatchOvertakenSequence_LeavesALiveSequenceAlone(t *testing.T) {
	t.Parallel()

	var running, head atomic.Uint64

	running.Store(8132286)
	head.Store(8132285)

	cancelCh, stop := runWatch(t, &running, &head)
	defer stop()

	select {
	case <-cancelCh:
		require.Fail(t, "canceled a sequence the chain has not reached")
	case <-time.After(300 * time.Millisecond):
	}
}

func TestWatchOvertakenSequence_NoSequenceNoCancel(t *testing.T) {
	t.Parallel()

	var running, head atomic.Uint64

	head.Store(8132285)

	cancelCh, stop := runWatch(t, &running, &head)
	defer stop()

	select {
	case <-cancelCh:
		require.Fail(t, "canceled while no sequence was running")
	case <-time.After(300 * time.Millisecond):
	}
}

func TestWatchOvertakenSequence_NormalAdvanceInsideGraceIsNotCanceled(t *testing.T) {
	t.Parallel()

	var running, head atomic.Uint64

	running.Store(10)
	head.Store(10)

	cancelCh, stop := runWatch(t, &running, &head)
	defer stop()

	time.Sleep(25 * time.Millisecond)
	running.Store(11)

	select {
	case <-cancelCh:
		require.Fail(t, "canceled the next sequence after a normal advance")
	case <-time.After(300 * time.Millisecond):
	}
}

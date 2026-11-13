package syncer

import (
	"errors"
	"testing"
	"time"

	"github.com/0xPolygon/polygon-edge/types"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/require"
)

// A sync that stops in the middle of a stream must not leave the stream's sender blocked for good
// (13.11 review): every failed attempt used to leak the goroutines and the gRPC stream they hold.
func Test_bulkSyncWithPeer_ReleasesTheStreamOnAnEarlyReturn(t *testing.T) {
	t.Parallel()

	blockCh := make(chan *types.Block) // unbuffered, like the client's hand-over
	senderDone := make(chan struct{})

	go func() {
		defer close(senderDone)
		defer close(blockCh)

		for n := uint64(1); n <= 5; n++ {
			blockCh <- &types.Block{Header: &types.Header{Number: n}}
		}
	}()

	writeErr := errors.New("block already inserted")
	syncer := NewTestSyncer(
		nil,
		&mockBlockchain{
			headerHandler: newSimpleHeaderHandler(0),
			verifyFinalizedBlockHandler: func(b *types.Block) (*types.FullBlock, error) {
				return &types.FullBlock{Block: b}, nil
			},
			writeFullBlockHandler: func(*types.FullBlock) error { return writeErr },
		},
		time.Second,
		&mockSyncPeerClient{
			getBlocksHandler: func(peer.ID, uint64, time.Duration) (<-chan *types.Block, error) {
				return blockCh, nil
			},
		},
		&mockProgression{},
	)

	_, _, err := syncer.bulkSyncWithPeer(peer.ID("X"), 5, func(*types.FullBlock) bool { return false })
	require.ErrorIs(t, err, writeErr)

	select {
	case <-senderDone:
	case <-time.After(2 * time.Second):
		t.Fatal("the stream's sender is still blocked after the sync returned")
	}
}

package syncer

import (
	"math/big"
	"sync"

	"github.com/libp2p/go-libp2p/core/peer"
)

type NoForkPeer struct {
	// identifier
	ID peer.ID
	// peer's latest block number
	Number uint64
	// peer's distance
	Distance *big.Int
}

func (p *NoForkPeer) IsBetter(t *NoForkPeer) bool {
	if p.Number != t.Number {
		return p.Number > t.Number
	}

	return p.Distance.Cmp(t.Distance) < 0
}

type PeerMap struct {
	sync.Map
}

func NewPeerMap(peers []*NoForkPeer) *PeerMap {
	peerMap := new(PeerMap)

	peerMap.Put(peers...)

	return peerMap
}

func (m *PeerMap) Put(peers ...*NoForkPeer) {
	for _, peer := range peers {
		m.Store(peer.ID.String(), peer)
	}
}

// Heights returns the latest block each known peer has announced, keyed by peer id.
// The map is a snapshot: entries the syncer learns about later are not reflected.
func (m *PeerMap) Heights() map[string]uint64 {
	heights := make(map[string]uint64)

	m.Range(func(_, value interface{}) bool {
		if peer, ok := value.(*NoForkPeer); ok {
			heights[peer.ID.String()] = peer.Number
		}

		return true
	})

	return heights
}

// Remove removes a peer from heap if it exists
func (m *PeerMap) Remove(peerID peer.ID) {
	m.Delete(peerID.String())
}

// BestPeer returns the top of heap
func (m *PeerMap) BestPeer(skipMap map[peer.ID]bool) *NoForkPeer {
	var bestPeer *NoForkPeer

	m.Range(func(key, value interface{}) bool {
		peer, _ := value.(*NoForkPeer)

		if skipMap != nil && skipMap[peer.ID] {
			return true
		}

		if bestPeer == nil || peer.IsBetter(bestPeer) {
			bestPeer = peer
		}

		return true
	})

	return bestPeer
}

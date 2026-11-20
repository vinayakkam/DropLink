package mock

import (
	"context"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/vinayakkam/droplink/internal/contract"
)

// Discovery is a scripted radar: tests and the UI demo add and remove peers
// by hand with Add and Remove.
type Discovery struct {
	mu    sync.Mutex
	peers map[contract.DeviceID]contract.Peer
	subs  map[chan contract.PeerEvent]struct{}
}

// NewDiscovery returns a Discovery that already knows peers.
func NewDiscovery(peers ...contract.Peer) *Discovery {
	d := &Discovery{
		peers: map[contract.DeviceID]contract.Peer{},
		subs:  map[chan contract.PeerEvent]struct{}{},
	}
	for _, p := range peers {
		d.peers[p.ID] = p
	}
	return d
}

// Run blocks until ctx ends, like a real discovery loop.
func (d *Discovery) Run(ctx context.Context) error {
	<-ctx.Done()
	return nil
}

// Peers returns known peers sorted by name.
func (d *Discovery) Peers() []contract.Peer {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]contract.Peer, 0, len(d.peers))
	for _, p := range d.peers {
		out = append(out, p)
	}
	slices.SortFunc(out, func(a, b contract.Peer) int { return strings.Compare(a.Name, b.Name) })
	return out
}

// Subscribe returns a buffered event channel. Events beyond the buffer are
// dropped for that subscriber, matching the contract.
func (d *Discovery) Subscribe() (<-chan contract.PeerEvent, func()) {
	ch := make(chan contract.PeerEvent, 16)
	d.mu.Lock()
	d.subs[ch] = struct{}{}
	d.mu.Unlock()
	var once sync.Once
	return ch, func() {
		once.Do(func() {
			d.mu.Lock()
			delete(d.subs, ch)
			d.mu.Unlock()
			close(ch)
		})
	}
}

// Add puts p on the radar (joined, or updated if already known).
func (d *Discovery) Add(p contract.Peer) {
	if p.LastSeen.IsZero() {
		p.LastSeen = time.Now()
	}
	d.mu.Lock()
	kind := contract.PeerJoined
	if _, ok := d.peers[p.ID]; ok {
		kind = contract.PeerUpdated
	}
	d.peers[p.ID] = p
	d.broadcast(contract.PeerEvent{Kind: kind, Peer: p})
	d.mu.Unlock()
}

// Remove takes a peer off the radar.
func (d *Discovery) Remove(id contract.DeviceID) {
	d.mu.Lock()
	if p, ok := d.peers[id]; ok {
		delete(d.peers, id)
		d.broadcast(contract.PeerEvent{Kind: contract.PeerLeft, Peer: p})
	}
	d.mu.Unlock()
}

// broadcast must be called with d.mu held.
func (d *Discovery) broadcast(ev contract.PeerEvent) {
	for ch := range d.subs {
		select {
		case ch <- ev:
		default: // slow subscriber: drop, never block
		}
	}
}

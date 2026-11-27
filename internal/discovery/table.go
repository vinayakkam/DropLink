package discovery

import (
	"net"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/vinayakkam/droplink/internal/contract"
)

var _ contract.Discovery = (*Discovery)(nil)

// subBuffer is how many events a subscriber may fall behind before
// events are dropped for it.
const subBuffer = 32

type entry struct {
	peer  contract.Peer
	nonce uint64
}

// table is the radar: peers by ID, with expiry and event fan-out.
type table struct {
	self contract.DeviceID
	ttl  time.Duration
	now  func() time.Time

	mu    sync.Mutex
	peers map[contract.DeviceID]entry
	subs  map[chan contract.PeerEvent]struct{}
}

func newTable(self contract.DeviceID, ttl time.Duration) *table {
	return &table{
		self: self, ttl: ttl, now: time.Now,
		peers: map[contract.DeviceID]entry{},
		subs:  map[chan contract.PeerEvent]struct{}{},
	}
}

// observe records a beacon heard from ip.
func (t *table) observe(b Beacon, ip net.IP) {
	if b.ID == t.self {
		return // our own beacon, looped back
	}
	now := t.now()
	p := beaconPeer(b, ip, now)
	t.mu.Lock()
	defer t.mu.Unlock()
	old, known := t.peers[b.ID]
	t.peers[b.ID] = entry{peer: p, nonce: b.Nonce}
	switch {
	case !known:
		t.emit(contract.PeerEvent{Kind: contract.PeerJoined, Peer: p})
	case old.nonce != b.Nonce || old.peer.Name != p.Name || old.peer.Avatar != p.Avatar || old.peer.Addr != p.Addr:
		// Restarted (new nonce), renamed, new avatar, or moved address.
		t.emit(contract.PeerEvent{Kind: contract.PeerUpdated, Peer: p})
	}
}

// expire drops peers not heard from within the TTL.
func (t *table) expire() {
	cutoff := t.now().Add(-t.ttl)
	t.mu.Lock()
	defer t.mu.Unlock()
	for id, e := range t.peers {
		if e.peer.LastSeen.Before(cutoff) {
			delete(t.peers, id)
			t.emit(contract.PeerEvent{Kind: contract.PeerLeft, Peer: e.peer})
		}
	}
}

// emit must be called with t.mu held. It never blocks.
func (t *table) emit(ev contract.PeerEvent) {
	for ch := range t.subs {
		select {
		case ch <- ev:
		default:
		}
	}
}

func (t *table) list() []contract.Peer {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]contract.Peer, 0, len(t.peers))
	for _, e := range t.peers {
		out = append(out, e.peer)
	}
	slices.SortFunc(out, func(a, b contract.Peer) int {
		if c := strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)); c != 0 {
			return c
		}
		return strings.Compare(string(a.ID), string(b.ID))
	})
	return out
}

func (t *table) subscribe() (<-chan contract.PeerEvent, func()) {
	ch := make(chan contract.PeerEvent, subBuffer)
	t.mu.Lock()
	t.subs[ch] = struct{}{}
	t.mu.Unlock()
	var once sync.Once
	return ch, func() {
		once.Do(func() {
			t.mu.Lock()
			delete(t.subs, ch)
			t.mu.Unlock()
			close(ch)
		})
	}
}

// Peers returns peers heard from within the TTL, sorted by name.
func (d *Discovery) Peers() []contract.Peer { return d.table.list() }

// Subscribe delivers joined/updated/left events. A subscriber more than
// 32 events behind loses events; discovery never waits for it.
func (d *Discovery) Subscribe() (<-chan contract.PeerEvent, func()) { return d.table.subscribe() }

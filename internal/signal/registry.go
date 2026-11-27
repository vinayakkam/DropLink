// Package signal is DropLink's signaling: a tiny hub that relays WebRTC
// setup messages between the devices in a room, and the client the
// sidecar uses to reach it. The hub keeps rooms in memory only and never
// stores or logs what it relays.
package signal

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/vinayakkam/droplink/internal/contract"
)

// Room codes use letters and digits that cannot be confused (no 0/O,
// 1/I/L).
const (
	CodeAlphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"
	CodeLen      = 6
)

// Limits protect the hub. Zero values mean the defaults.
type Limits struct {
	MaxMembers int           // per room (8)
	MaxRooms   int           // per hub (10 000)
	Idle       time.Duration // a room with no message this long is closed (10 min)
	Outbox     int           // queued messages per member before it is dropped (64)
}

func (l *Limits) defaults() {
	if l.MaxMembers <= 0 {
		l.MaxMembers = 8
	}
	if l.MaxRooms <= 0 {
		l.MaxRooms = 10_000
	}
	if l.Idle <= 0 {
		l.Idle = 10 * time.Minute
	}
	if l.Outbox <= 0 {
		l.Outbox = 64
	}
}

// Errors a join can fail with. The server sends their text to the client.
var (
	ErrNoRoom    = errors.New("signal: no such room")
	ErrRoomFull  = errors.New("signal: room is full")
	ErrHubFull   = errors.New("signal: too many rooms")
	ErrDuplicate = errors.New("signal: device already in this room")
	ErrBadCode   = errors.New("signal: malformed room code")
)

// Registry holds every live room.
type Registry struct {
	lim Limits
	now func() time.Time

	mu    sync.Mutex
	rooms map[string]*room
}

type room struct {
	code    string
	members map[contract.DeviceID]*Member
	last    time.Time
}

// Member is one connected device. The server drains Out and writes it to
// the socket; Gone is closed when the member is removed.
type Member struct {
	Peer contract.Peer
	Out  chan contract.Signal
	Gone chan struct{}
	code string
	once sync.Once
}

func (m *Member) drop() { m.once.Do(func() { close(m.Gone) }) }

// NewRegistry returns an empty registry.
func NewRegistry(lim Limits) *Registry {
	lim.defaults()
	return &Registry{lim: lim, now: time.Now, rooms: map[string]*room{}}
}

// ValidCode reports whether s looks like a room code.
func ValidCode(s string) bool {
	if len(s) != CodeLen {
		return false
	}
	for i := 0; i < len(s); i++ {
		found := false
		for j := 0; j < len(CodeAlphabet); j++ {
			if s[i] == CodeAlphabet[j] {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func newCode() string {
	b := make([]byte, CodeLen)
	_, _ = rand.Read(b)
	for i := range b {
		// 31 symbols: the bias from 256 % 31 is negligible for codes that
		// live minutes and are guarded by the member limit.
		b[i] = CodeAlphabet[int(b[i])%len(CodeAlphabet)]
	}
	return string(b)
}

// Join adds p to room code ("" creates a new room) and returns the member
// and the peers already there. Everyone else gets a hello.
func (r *Registry) Join(code string, p contract.Peer) (*Member, []contract.Peer, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if code == "" {
		if len(r.rooms) >= r.lim.MaxRooms {
			return nil, nil, ErrHubFull
		}
		for code == "" || r.rooms[code] != nil {
			code = newCode()
		}
		r.rooms[code] = &room{code: code, members: map[contract.DeviceID]*Member{}, last: r.now()}
	} else if !ValidCode(code) {
		return nil, nil, ErrBadCode
	}
	rm, ok := r.rooms[code]
	if !ok {
		return nil, nil, ErrNoRoom
	}
	if _, dup := rm.members[p.ID]; dup {
		return nil, nil, ErrDuplicate
	}
	if len(rm.members) >= r.lim.MaxMembers {
		return nil, nil, ErrRoomFull
	}
	others := make([]contract.Peer, 0, len(rm.members))
	for _, m := range rm.members {
		others = append(others, m.Peer)
		r.deliver(m, contract.Signal{Kind: contract.SignalHello, From: p.ID, Data: peerJSON(p)})
	}
	m := &Member{Peer: p, Out: make(chan contract.Signal, r.lim.Outbox), Gone: make(chan struct{}), code: code}
	rm.members[p.ID] = m
	rm.last = r.now()
	return m, others, nil
}

// Code returns the room the member is in.
func (m *Member) Code() string { return m.code }

// Relay routes s from m: to s.To, or to everyone else when To is empty.
// From is always set by the hub, never trusted from the sender.
func (r *Registry) Relay(m *Member, s contract.Signal) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	rm, ok := r.rooms[m.code]
	if !ok || rm.members[m.Peer.ID] != m {
		return ErrNoRoom
	}
	s.From = m.Peer.ID
	rm.last = r.now()
	for id, other := range rm.members {
		if other == m || (s.To != "" && s.To != id) {
			continue
		}
		r.deliver(other, s)
	}
	return nil
}

// Leave removes m and tells the others. An empty room is deleted at once.
func (r *Registry) Leave(m *Member) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.leaveLocked(m)
}

func (r *Registry) leaveLocked(m *Member) {
	m.drop()
	rm, ok := r.rooms[m.code]
	if !ok || rm.members[m.Peer.ID] != m {
		return
	}
	delete(rm.members, m.Peer.ID)
	for _, other := range rm.members {
		r.deliver(other, contract.Signal{Kind: contract.SignalBye, From: m.Peer.ID})
	}
	if len(rm.members) == 0 {
		delete(r.rooms, m.code)
	}
}

// Sweep closes rooms idle longer than Limits.Idle. The server calls it
// periodically.
func (r *Registry) Sweep() {
	cutoff := r.now().Add(-r.lim.Idle)
	r.mu.Lock()
	defer r.mu.Unlock()
	for code, rm := range r.rooms {
		if rm.last.Before(cutoff) {
			for _, m := range rm.members {
				m.drop()
			}
			delete(r.rooms, code)
		}
	}
}

// Stats is for health checks: counts only, never contents.
func (r *Registry) Stats() (rooms, members int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, rm := range r.rooms {
		members += len(rm.members)
	}
	return len(r.rooms), members
}

// deliver must be called with r.mu held. A member whose outbox is full is
// too slow (or stuck) and is dropped instead of blocking the room.
func (r *Registry) deliver(m *Member, s contract.Signal) {
	select {
	case m.Out <- s:
	default:
		m.drop()
	}
}

// peerJSON is a hello's payload: who joined, so the others can list them.
func peerJSON(p contract.Peer) []byte {
	b, _ := json.Marshal(contract.Peer{ID: p.ID, Name: p.Name, Avatar: p.Avatar, Route: contract.RouteWebRTC})
	return b
}

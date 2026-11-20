package mock

import (
	"context"
	"crypto/rand"
	"errors"
	"sync"

	"github.com/vinayakkam/droplink/internal/contract"
)

// Hub is an in-process signaling hub: rooms live in memory, signals are
// relayed to members and never kept.
type Hub struct {
	mu    sync.Mutex
	rooms map[string]map[contract.DeviceID]*room
}

// NewHub returns an empty hub.
func NewHub() *Hub { return &Hub{rooms: map[string]map[contract.DeviceID]*room{}} }

// ErrNoRoom is returned when joining a code that does not exist.
var ErrNoRoom = errors.New("mock: no such room")

// Join creates a room when code is "", else joins an existing one.
func (h *Hub) Join(ctx context.Context, code string, self contract.Peer) (contract.Room, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if code == "" {
		code = newCode()
		h.rooms[code] = map[contract.DeviceID]*room{}
	}
	members, ok := h.rooms[code]
	if !ok {
		return nil, ErrNoRoom
	}
	r := &room{hub: h, code: code, self: self, in: make(chan contract.Signal, 32)}
	members[self.ID] = r
	for _, other := range members {
		if other != r {
			other.deliver(contract.Signal{Kind: contract.SignalHello, From: self.ID})
		}
	}
	return r, nil
}

// codeAlphabet has no look-alike characters (0/O, 1/I/L).
const codeAlphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"

func newCode() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	for i := range b {
		b[i] = codeAlphabet[int(b[i])%len(codeAlphabet)]
	}
	return string(b)
}

type room struct {
	hub    *Hub
	code   string
	self   contract.Peer
	in     chan contract.Signal
	mu     sync.Mutex
	closed bool
}

func (r *room) Code() string { return r.code }

func (r *room) Members() []contract.Peer {
	r.hub.mu.Lock()
	defer r.hub.mu.Unlock()
	var out []contract.Peer
	for _, m := range r.hub.rooms[r.code] {
		out = append(out, m.self)
	}
	return out
}

func (r *room) Send(ctx context.Context, s contract.Signal) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.From = r.self.ID
	r.hub.mu.Lock()
	defer r.hub.mu.Unlock()
	members, ok := r.hub.rooms[r.code]
	if !ok || members[r.self.ID] != r {
		return ErrNoRoom
	}
	for id, m := range members {
		if m == r || (s.To != "" && s.To != id) {
			continue
		}
		m.deliver(s)
	}
	return nil
}

func (r *room) Signals() <-chan contract.Signal { return r.in }

// Close leaves the room, says bye to the others and deletes an empty room.
func (r *room) Close() error {
	r.hub.mu.Lock()
	if members, ok := r.hub.rooms[r.code]; ok && members[r.self.ID] == r {
		delete(members, r.self.ID)
		for _, m := range members {
			m.deliver(contract.Signal{Kind: contract.SignalBye, From: r.self.ID})
		}
		if len(members) == 0 {
			delete(r.hub.rooms, r.code)
		}
	}
	r.hub.mu.Unlock()

	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.closed {
		r.closed = true
		close(r.in)
	}
	return nil
}

// deliver never blocks the hub: a full inbox drops the signal.
func (r *room) deliver(s contract.Signal) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return
	}
	select {
	case r.in <- s:
	default:
	}
}

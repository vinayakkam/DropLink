package mock

import (
	"context"
	"errors"
	"net"
	"sync"
	"time"

	"github.com/vinayakkam/droplink/internal/contract"
)

var timeZero time.Time

// ErrClosed is returned by a closed Transport.
var ErrClosed = errors.New("mock: transport closed")

// Network connects mock Transports in one process. Dial on one transport
// shows up as Accept on the transport registered for the peer's ID.
type Network struct {
	mu    sync.Mutex
	nodes map[contract.DeviceID]*Transport
}

// NewNetwork returns an empty network.
func NewNetwork() *Network {
	return &Network{nodes: map[contract.DeviceID]*Transport{}}
}

// Transport is one device's endpoint on a Network.
type Transport struct {
	net      *Network
	self     contract.Peer
	route    contract.Route
	incoming chan contract.Stream
	done     chan struct{}
	once     sync.Once
}

// Join registers self on the network and returns its transport.
func (n *Network) Join(self contract.Peer, route contract.Route) *Transport {
	t := &Transport{
		net:      n,
		self:     self,
		route:    route,
		incoming: make(chan contract.Stream),
		done:     make(chan struct{}),
	}
	n.mu.Lock()
	n.nodes[self.ID] = t
	n.mu.Unlock()
	return t
}

func (t *Transport) Route() contract.Route { return t.route }

// Dial opens a net.Pipe to the peer. The far end receives it from Accept.
func (t *Transport) Dial(ctx context.Context, to contract.Peer) (contract.Stream, error) {
	t.net.mu.Lock()
	far, ok := t.net.nodes[to.ID]
	t.net.mu.Unlock()
	if !ok {
		return nil, errors.New("mock: no such peer " + string(to.ID))
	}
	a, b := net.Pipe()
	select {
	case far.incoming <- &stream{Conn: b, remote: t.self}:
		return &stream{Conn: a, remote: to}, nil
	case <-far.done:
		a.Close()
		b.Close()
		return nil, ErrClosed
	case <-t.done:
		a.Close()
		b.Close()
		return nil, ErrClosed
	case <-ctx.Done():
		a.Close()
		b.Close()
		return nil, ctx.Err()
	}
}

func (t *Transport) Accept(ctx context.Context) (contract.Stream, error) {
	select {
	case s := <-t.incoming:
		return s, nil
	case <-t.done:
		return nil, ErrClosed
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Close unregisters the transport and fails pending Accept and Dial calls.
func (t *Transport) Close() error {
	t.once.Do(func() {
		close(t.done)
		t.net.mu.Lock()
		if t.net.nodes[t.self.ID] == t {
			delete(t.net.nodes, t.self.ID)
		}
		t.net.mu.Unlock()
	})
	return nil
}

type stream struct {
	net.Conn
	remote contract.Peer
}

func (s *stream) Remote() contract.Peer { return s.remote }

// Pipe returns two connected Streams, for engine tests that need no Network.
func Pipe(a, b contract.Peer) (contract.Stream, contract.Stream) {
	x, y := net.Pipe()
	return &stream{Conn: x, remote: b}, &stream{Conn: y, remote: a}
}

// Package lan implements contract.Transport for route "lan": TCP streams
// between devices on the same network.
package lan

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/vinayakkam/droplink/internal/contract"
)

// ErrClosed is returned by a closed Transport.
var ErrClosed = errors.New("lan: transport closed")

// ErrWrongPeer means the device that answered is not the one we dialed:
// its identity key does not match the ID discovery reported.
var ErrWrongPeer = errors.New("lan: peer identity mismatch")

// Options tune a Transport. Zero values mean the defaults.
type Options struct {
	Addr             string        // listen address (":0" = any free port)
	DialTimeout      time.Duration // TCP connect timeout (5 s)
	HandshakeTimeout time.Duration // per-connection setup bound (10 s)
	MaxPending       int           // accepted streams waiting for Accept (16)
	NoCompress       bool          // never use zstd (both ends must agree to use it)
	OnPort           func(uint16)  // called once with the listening port
	Log              *slog.Logger
}

func (o *Options) defaults() {
	if o.Addr == "" {
		o.Addr = ":0"
	}
	if o.DialTimeout <= 0 {
		o.DialTimeout = 5 * time.Second
	}
	if o.HandshakeTimeout <= 0 {
		o.HandshakeTimeout = 10 * time.Second
	}
	if o.MaxPending <= 0 {
		o.MaxPending = 16
	}
	if o.Log == nil {
		o.Log = slog.New(slog.DiscardHandler)
	}
}

// Transport listens for and dials LAN streams.
type Transport struct {
	self   contract.Peer
	crypto contract.Crypto
	opts   Options

	ln       net.Listener
	incoming chan contract.Stream
	done     chan struct{}
	once     sync.Once
	wg       sync.WaitGroup

	mu      sync.Mutex
	streams map[*stream]struct{}

	limit *limiter
}

var _ contract.Transport = (*Transport)(nil)

// New starts listening. Every stream is secured with crypto. Call Close
// to stop.
func New(self contract.Peer, crypto contract.Crypto, opts Options) (*Transport, error) {
	if crypto == nil {
		return nil, errors.New("lan: crypto is required")
	}
	opts.defaults()
	ln, err := net.Listen("tcp", opts.Addr)
	if err != nil {
		return nil, fmt.Errorf("lan: listen %s: %w", opts.Addr, err)
	}
	t := &Transport{
		self:     self,
		crypto:   crypto,
		opts:     opts,
		ln:       ln,
		incoming: make(chan contract.Stream, opts.MaxPending),
		done:     make(chan struct{}),
		streams:  map[*stream]struct{}{},
		limit:    newLimiter(),
	}
	if opts.OnPort != nil {
		opts.OnPort(t.Port())
	}
	t.wg.Add(1)
	go t.acceptLoop()
	return t, nil
}

// Port is the TCP port other devices dial.
func (t *Transport) Port() uint16 {
	_, p, _ := net.SplitHostPort(t.ln.Addr().String())
	n, _ := strconv.Atoi(p)
	return uint16(n)
}

func (t *Transport) Route() contract.Route { return contract.RouteLAN }

func (t *Transport) acceptLoop() {
	defer t.wg.Done()
	for {
		c, err := t.ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			t.opts.Log.Debug("lan: accept", "err", err)
			select {
			case <-t.done:
				return
			case <-time.After(50 * time.Millisecond): // e.g. EMFILE: back off
			}
			continue
		}
		t.wg.Add(1)
		go func() {
			defer t.wg.Done()
			t.handle(c)
		}()
	}
}

// handle sets up one incoming connection and queues it for Accept.
func (t *Transport) handle(c net.Conn) {
	ctx, cancel := t.setupContext()
	defer cancel()
	s, err := t.setup(ctx, c, false, contract.Peer{Route: contract.RouteLAN, Addr: c.RemoteAddr().String()})
	if err != nil {
		t.opts.Log.Debug("lan: incoming setup failed", "from", c.RemoteAddr(), "err", err)
		c.Close()
		return
	}
	select {
	case t.incoming <- s:
	default:
		// Nobody is accepting and the queue is full: refuse rather than
		// pile up connections.
		s.Close()
	}
}

// setupContext bounds connection setup and ends it when t closes.
func (t *Transport) setupContext() (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(context.Background(), t.opts.HandshakeTimeout)
	go func() { // ends as soon as either side is done
		select {
		case <-t.done:
			cancel()
		case <-ctx.Done():
		}
	}()
	return ctx, cancel
}

// Dial connects to a peer seen by discovery (to.Addr must be set).
func (t *Transport) Dial(ctx context.Context, to contract.Peer) (contract.Stream, error) {
	select {
	case <-t.done:
		return nil, ErrClosed
	default:
	}
	if to.Addr == "" {
		return nil, fmt.Errorf("lan: %s has no LAN address", to.ID)
	}
	d := net.Dialer{Timeout: t.opts.DialTimeout, KeepAlive: 15 * time.Second}
	c, err := d.DialContext(ctx, "tcp", to.Addr)
	if err != nil {
		return nil, fmt.Errorf("lan: dial %s: %w", to.Addr, err)
	}
	ctx, cancel := context.WithTimeout(ctx, t.opts.HandshakeTimeout)
	defer cancel()
	to.Route = contract.RouteLAN
	s, err := t.setup(ctx, c, true, to)
	if err != nil {
		c.Close()
		return nil, err
	}
	return s, nil
}

// Accept returns the next incoming stream.
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

// Close stops listening, closes every open stream and waits for the
// transport's goroutines.
func (t *Transport) Close() error {
	var err error
	t.once.Do(func() {
		close(t.done)
		err = t.ln.Close()
		t.mu.Lock()
		open := make([]*stream, 0, len(t.streams))
		for s := range t.streams {
			open = append(open, s)
		}
		t.mu.Unlock()
		for _, s := range open {
			s.Close()
		}
		t.wg.Wait()
		for {
			select {
			case s := <-t.incoming:
				s.Close()
			default:
				return
			}
		}
	})
	return err
}

// setup turns a raw connection into a Stream.
func (t *Transport) setup(ctx context.Context, c net.Conn, initiator bool, remote contract.Peer) (*stream, error) {
	if tc, ok := c.(*net.TCPConn); ok {
		_ = tc.SetNoDelay(true)
	}
	sc, err := t.crypto.Secure(ctx, c, initiator)
	if err != nil {
		return nil, fmt.Errorf("lan: handshake with %s: %w", c.RemoteAddr(), err)
	}
	if initiator {
		if sc.RemoteID() != remote.ID {
			return nil, fmt.Errorf("%w: dialed %s, reached %s", ErrWrongPeer, remote.ID, sc.RemoteID())
		}
	} else {
		remote.ID = sc.RemoteID()
		remote.Trusted = t.crypto.IsTrusted(remote.ID)
	}
	want := flagZstd
	if t.opts.NoCompress {
		want = 0
	}
	flags, err := negotiate(sc, initiator, want)
	if err != nil {
		return nil, fmt.Errorf("lan: negotiate with %s: %w", c.RemoteAddr(), err)
	}
	s := &stream{conn: sc, sas: sc.SAS(), r: sc, w: sc, remote: remote, compressed: flags&flagZstd != 0,
		closed: make(chan struct{})}
	if s.compressed {
		if err := compress(s); err != nil {
			return nil, err
		}
	}
	s.w = limitWriter{l: t.limit, w: s.w, closed: s.closed}
	return t.track(s), nil
}

func (t *Transport) track(s *stream) *stream {
	s.t = t
	t.mu.Lock()
	t.streams[s] = struct{}{}
	t.mu.Unlock()
	return s
}

func (t *Transport) forget(s *stream) {
	t.mu.Lock()
	delete(t.streams, s)
	t.mu.Unlock()
}

// stream is one LAN connection. Close unblocks pending Read and Write.
type stream struct {
	conn   net.Conn
	sas    contract.SAS
	r      io.Reader
	w      io.Writer
	extra  []io.Closer // compression state etc., closed after conn
	remote contract.Peer
	t      *Transport
	once   sync.Once

	compressed bool
	closed     chan struct{} // closed by Close; ends speed-limit waits
}

func (s *stream) Read(p []byte) (int, error)  { return s.r.Read(p) }
func (s *stream) Write(p []byte) (int, error) { return s.w.Write(p) }
func (s *stream) Remote() contract.Peer       { return s.remote }

// SAS is the 4-glyph code of this stream's handshake. The manager shows
// it in the offer dialog (type-assert for interface{ SAS() contract.SAS }).
func (s *stream) SAS() contract.SAS { return s.sas }

func (s *stream) Close() error {
	var err error
	s.once.Do(func() {
		if s.closed != nil {
			close(s.closed)
		}
		err = s.conn.Close()
		for _, c := range s.extra {
			_ = c.Close()
		}
		s.t.forget(s)
	})
	return err
}

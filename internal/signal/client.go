package signal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/vinayakkam/droplink/internal/contract"
)

// Client implements contract.Signaler against a hub at URL
// (ws://host:port/ws or wss://...).
type Client struct {
	URL string
	// Backoff between reconnect attempts; the room is given up after the
	// last one. Default 0.5, 1, 2, 4, 8 s.
	Backoff []time.Duration
}

var _ contract.Signaler = (*Client)(nil)

// New returns a Client for the hub at url.
func New(url string) *Client { return &Client{URL: url} }

func (c *Client) backoff() []time.Duration {
	if len(c.Backoff) > 0 {
		return c.Backoff
	}
	return []time.Duration{500 * time.Millisecond, time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second}
}

// knownErrs maps the hub's error text back to sentinel errors.
var knownErrs = []error{ErrNoRoom, ErrRoomFull, ErrHubFull, ErrDuplicate, ErrBadCode}

func (c *Client) connect(ctx context.Context, code string, self contract.Peer) (*websocket.Conn, Envelope, error) {
	u, err := url.Parse(c.URL)
	if err != nil {
		return nil, Envelope{}, fmt.Errorf("signal: hub url: %w", err)
	}
	q := u.Query()
	q.Set("room", code)
	q.Set("id", string(self.ID))
	q.Set("name", self.Name)
	q.Set("avatar", strconv.Itoa(int(self.Avatar&15)))
	u.RawQuery = q.Encode()

	dctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(dctx, u.String(), nil)
	if err != nil {
		return nil, Envelope{}, fmt.Errorf("signal: connect: %w", err)
	}
	conn.SetReadLimit(1 << 20)
	_, b, err := conn.Read(dctx)
	if err != nil {
		conn.CloseNow()
		return nil, Envelope{}, fmt.Errorf("signal: no welcome: %w", err)
	}
	var env Envelope
	if err := json.Unmarshal(b, &env); err != nil {
		conn.CloseNow()
		return nil, env, fmt.Errorf("signal: bad welcome: %w", err)
	}
	if env.Type == "error" {
		conn.CloseNow()
		for _, k := range knownErrs {
			if env.Error == k.Error() {
				return nil, env, k
			}
		}
		return nil, env, errors.New(env.Error)
	}
	if env.Type != "welcome" || !ValidCode(env.Code) {
		conn.CloseNow()
		return nil, env, fmt.Errorf("signal: unexpected %q from hub", env.Type)
	}
	return conn, env, nil
}

// Join creates (code "") or joins a room.
func (c *Client) Join(ctx context.Context, code string, self contract.Peer) (contract.Room, error) {
	conn, env, err := c.connect(ctx, strings.ToUpper(code), self)
	if err != nil {
		return nil, err
	}
	r := &clientRoom{
		c: c, code: env.Code, self: self,
		conn:    conn,
		members: map[contract.DeviceID]contract.Peer{},
		in:      make(chan contract.Signal, 64),
		done:    make(chan struct{}),
		exited:  make(chan struct{}),
	}
	r.setMembers(env.Members)
	go r.loop()
	return r, nil
}

type clientRoom struct {
	c    *Client
	code string
	self contract.Peer

	mu      sync.Mutex
	conn    *websocket.Conn // nil while reconnecting
	members map[contract.DeviceID]contract.Peer

	in     chan contract.Signal
	done   chan struct{}
	exited chan struct{}
	once   sync.Once
}

func (r *clientRoom) Code() string { return r.code }

func (r *clientRoom) Members() []contract.Peer {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := []contract.Peer{r.self}
	for _, p := range r.members {
		out = append(out, p)
	}
	slices.SortFunc(out, func(a, b contract.Peer) int { return strings.Compare(string(a.ID), string(b.ID)) })
	return out
}

func (r *clientRoom) setMembers(ps []contract.Peer) {
	r.mu.Lock()
	defer r.mu.Unlock()
	clear(r.members)
	for _, p := range ps {
		r.members[p.ID] = p
	}
}

func (r *clientRoom) Signals() <-chan contract.Signal { return r.in }

// Send relays s through the hub. From is set by the hub.
func (r *clientRoom) Send(ctx context.Context, s contract.Signal) error {
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	r.mu.Lock()
	conn := r.conn
	r.mu.Unlock()
	if conn == nil {
		select {
		case <-r.done:
			return ErrNoRoom
		default:
			return errors.New("signal: reconnecting to the hub")
		}
	}
	return conn.Write(ctx, websocket.MessageText, b)
}

// Close leaves the room. Signals is closed once the reader has stopped.
func (r *clientRoom) Close() error {
	r.once.Do(func() {
		close(r.done)
		r.mu.Lock()
		if r.conn != nil {
			r.conn.Close(websocket.StatusNormalClosure, "bye")
		}
		r.mu.Unlock()
	})
	<-r.exited
	return nil
}

// loop reads from the hub and reconnects while the room lives. It owns
// r.in and closes it when it ends.
func (r *clientRoom) loop() {
	defer close(r.exited)
	defer close(r.in)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		select {
		case <-r.done:
			cancel()
		case <-ctx.Done():
		}
	}()

	for {
		r.mu.Lock()
		conn := r.conn
		r.mu.Unlock()
		r.read(ctx, conn)
		conn.CloseNow()
		r.mu.Lock()
		r.conn = nil
		r.mu.Unlock()
		if !r.reconnect(ctx) {
			return
		}
	}
}

func (r *clientRoom) read(ctx context.Context, conn *websocket.Conn) {
	for {
		_, b, err := conn.Read(ctx)
		if err != nil {
			return
		}
		var env Envelope
		if json.Unmarshal(b, &env) != nil || env.Type != "signal" || env.Signal == nil {
			continue
		}
		s := *env.Signal
		switch s.Kind {
		case contract.SignalHello:
			var p contract.Peer
			if json.Unmarshal(s.Data, &p) == nil && p.ID == s.From {
				r.mu.Lock()
				r.members[p.ID] = p
				r.mu.Unlock()
			}
		case contract.SignalBye:
			r.mu.Lock()
			delete(r.members, s.From)
			r.mu.Unlock()
		}
		select {
		case r.in <- s:
		case <-ctx.Done():
			return
		}
	}
}

// reconnect rejoins the same room after a dropped connection. It gives up
// when the room is gone, the backoff is used up, or Close was called.
func (r *clientRoom) reconnect(ctx context.Context) bool {
	for _, wait := range r.c.backoff() {
		select {
		case <-ctx.Done():
			return false
		case <-time.After(wait):
		}
		conn, env, err := r.c.connect(ctx, r.code, r.self)
		if errors.Is(err, ErrNoRoom) || errors.Is(err, ErrBadCode) {
			return false
		}
		if err != nil {
			continue
		}
		r.setMembers(env.Members)
		r.mu.Lock()
		select {
		case <-r.done: // closed while we were connecting
			r.mu.Unlock()
			conn.CloseNow()
			return false
		default:
		}
		r.conn = conn
		r.mu.Unlock()
		return true
	}
	return false
}

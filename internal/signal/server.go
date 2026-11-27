package signal

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/coder/websocket"

	"github.com/vinayakkam/droplink/internal/contract"
)

// Envelope is every message the hub sends:
//
//	{"type":"welcome","code":"ABC234","members":[...]}
//	{"type":"signal","signal":{...}}
//	{"type":"error","error":"signal: room is full"}
//
// Clients send bare contract.Signal JSON.
type Envelope struct {
	Type    string           `json:"type"`
	Code    string           `json:"code,omitempty"`
	Members []contract.Peer  `json:"members,omitempty"`
	Signal  *contract.Signal `json:"signal,omitempty"`
	Error   string           `json:"error,omitempty"`
}

// Server relays signals over WebSocket. Clients connect to
//
//	GET <path>?room=<code or empty>&id=<device id>&name=<name>&avatar=<0..15>
type Server struct {
	Reg        *Registry
	MaxMessage int64         // bytes per message (64 KiB)
	Rate       float64       // messages per second per connection (20)
	Burst      int           // (40)
	Ping       time.Duration // keep-alive (20 s)
	Log        *slog.Logger
}

// NewServer returns a Server with default limits.
func NewServer(reg *Registry) *Server {
	return &Server{Reg: reg, MaxMessage: 64 << 10, Rate: 20, Burst: 40, Ping: 20 * time.Second,
		Log: slog.New(slog.DiscardHandler)}
}

// Run sweeps idle rooms until ctx ends.
func (s *Server) Run(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.Reg.Sweep()
		}
	}
}

func parsePeer(r *http.Request) (contract.Peer, error) {
	q := r.URL.Query()
	p := contract.Peer{ID: contract.DeviceID(q.Get("id")), Name: q.Get("name"), Route: contract.RouteWebRTC}
	if !validID(p.ID) {
		return p, errors.New("signal: bad device id")
	}
	if len(p.Name) > 64 || !utf8.ValidString(p.Name) {
		return p, errors.New("signal: bad name")
	}
	if a := q.Get("avatar"); a != "" {
		n, err := strconv.Atoi(a)
		if err != nil || n < 0 || n > 15 {
			return p, errors.New("signal: bad avatar")
		}
		p.Avatar = uint8(n)
	}
	return p, nil
}

func validID(id contract.DeviceID) bool {
	if len(id) != 32 {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// ServeHTTP upgrades to WebSocket and runs one member until it leaves.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	peer, err := parsePeer(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	// No Origin check is needed: devices are not browsers, and a browser
	// page can learn nothing here that a room code does not already give.
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	defer c.CloseNow()
	c.SetReadLimit(s.MaxMessage)
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	m, others, err := s.Reg.Join(r.URL.Query().Get("room"), peer)
	if err != nil {
		_ = s.write(ctx, c, Envelope{Type: "error", Error: err.Error()})
		c.Close(websocket.StatusPolicyViolation, "join refused")
		return
	}
	defer s.Reg.Leave(m)
	if err := s.write(ctx, c, Envelope{Type: "welcome", Code: m.Code(), Members: others}); err != nil {
		return
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer cancel()
		s.writeLoop(ctx, c, m)
	}()
	s.readLoop(ctx, c, m)
	cancel()
	wg.Wait()
	c.Close(websocket.StatusNormalClosure, "")
}

func (s *Server) write(ctx context.Context, c *websocket.Conn, e Envelope) error {
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return c.Write(ctx, websocket.MessageText, b)
}

func (s *Server) writeLoop(ctx context.Context, c *websocket.Conn, m *Member) {
	ping := time.NewTicker(s.Ping)
	defer ping.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-m.Gone: // swept, or too slow to keep up
			return
		case sig := <-m.Out:
			if s.write(ctx, c, Envelope{Type: "signal", Signal: &sig}) != nil {
				return
			}
		case <-ping.C:
			pctx, done := context.WithTimeout(ctx, 10*time.Second)
			err := c.Ping(pctx)
			done()
			if err != nil {
				return
			}
		}
	}
}

func (s *Server) readLoop(ctx context.Context, c *websocket.Conn, m *Member) {
	tokens, last := float64(s.Burst), time.Now()
	for {
		typ, b, err := c.Read(ctx)
		if err != nil {
			return
		}
		now := time.Now()
		tokens = min(float64(s.Burst), tokens+now.Sub(last).Seconds()*s.Rate)
		last = now
		if tokens < 1 {
			c.Close(websocket.StatusPolicyViolation, "rate limit")
			return
		}
		tokens--
		if typ != websocket.MessageText {
			continue
		}
		var sig contract.Signal
		if json.Unmarshal(b, &sig) != nil {
			continue
		}
		switch sig.Kind {
		case contract.SignalOffer, contract.SignalAnswer, contract.SignalCandidate:
		default:
			continue // hello and bye come from the hub only
		}
		if s.Reg.Relay(m, sig) != nil {
			return
		}
	}
}

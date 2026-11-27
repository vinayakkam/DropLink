package signal

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/vinayakkam/droplink/internal/contract"
	"github.com/vinayakkam/droplink/internal/testutil"
)

func peer(c byte, name string) contract.Peer {
	return contract.Peer{ID: contract.DeviceID(strings.Repeat(string(c), 32)), Name: name}
}

func recvOut(t *testing.T, m *Member) contract.Signal {
	t.Helper()
	select {
	case s := <-m.Out:
		return s
	case <-time.After(2 * time.Second):
		t.Fatal("no signal")
	}
	return contract.Signal{}
}

func TestRegistryRooms(t *testing.T) {
	r := NewRegistry(Limits{MaxMembers: 2})
	a, others, err := r.Join("", peer('a', "A"))
	if err != nil || len(others) != 0 || !ValidCode(a.Code()) {
		t.Fatalf("create: %v %v %q", err, others, a.Code())
	}
	b, others, err := r.Join(a.Code(), peer('b', "B"))
	if err != nil || len(others) != 1 || others[0].ID != a.Peer.ID {
		t.Fatalf("join: %v %v", err, others)
	}
	hello := recvOut(t, a)
	var p contract.Peer
	if hello.Kind != contract.SignalHello || hello.From != b.Peer.ID || json.Unmarshal(hello.Data, &p) != nil || p.Name != "B" {
		t.Fatalf("hello = %+v", hello)
	}

	for name, tc := range map[string]struct {
		code string
		p    contract.Peer
		want error
	}{
		"full":      {a.Code(), peer('c', "C"), ErrRoomFull},
		"missing":   {"ZZZZZZ", peer('c', "C"), ErrNoRoom},
		"bad code":  {"abc", peer('c', "C"), ErrBadCode},
		"look-alik": {"OOOOOO", peer('c', "C"), ErrBadCode},
	} {
		if _, _, err := r.Join(tc.code, tc.p); !errors.Is(err, tc.want) {
			t.Errorf("%s: %v", name, err)
		}
	}

	// From is stamped by the hub; To routes to one member.
	_ = r.Relay(b, contract.Signal{Kind: contract.SignalOffer, From: "forged", To: a.Peer.ID, Data: []byte("sdp")})
	if s := recvOut(t, a); s.From != b.Peer.ID || string(s.Data) != "sdp" {
		t.Fatalf("relayed %+v", s)
	}
	select {
	case s := <-b.Out:
		t.Fatalf("sender got its own signal %+v", s)
	default:
	}

	r.Leave(b)
	if s := recvOut(t, a); s.Kind != contract.SignalBye || s.From != b.Peer.ID {
		t.Fatalf("bye = %+v", s)
	}
	if err := r.Relay(b, contract.Signal{Kind: contract.SignalOffer}); !errors.Is(err, ErrNoRoom) {
		t.Fatalf("relay after leave = %v", err)
	}
	r.Leave(a)
	if rooms, members := r.Stats(); rooms != 0 || members != 0 {
		t.Fatalf("left behind %d rooms, %d members", rooms, members)
	}
}

func TestRegistryDuplicateAndHubFull(t *testing.T) {
	r := NewRegistry(Limits{MaxRooms: 1})
	a, _, _ := r.Join("", peer('a', "A"))
	if _, _, err := r.Join(a.Code(), peer('a', "A again")); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("duplicate: %v", err)
	}
	if _, _, err := r.Join("", peer('b', "B")); !errors.Is(err, ErrHubFull) {
		t.Fatalf("hub full: %v", err)
	}
}

func TestRegistrySweepAndSlowMember(t *testing.T) {
	now := time.Unix(1000, 0)
	r := NewRegistry(Limits{Idle: time.Minute, Outbox: 1})
	r.now = func() time.Time { return now }
	a, _, _ := r.Join("", peer('a', "A"))
	b, _, _ := r.Join(a.Code(), peer('b', "B")) // a's outbox now holds the hello
	_ = r.Relay(b, contract.Signal{Kind: contract.SignalOffer})
	select {
	case <-a.Gone:
	default:
		t.Fatal("a slow member was not dropped")
	}

	now = now.Add(2 * time.Minute)
	r.Sweep()
	select {
	case <-b.Gone:
	default:
		t.Fatal("idle room not swept")
	}
	if rooms, _ := r.Stats(); rooms != 0 {
		t.Fatal("swept room still counted")
	}
}

func TestValidCode(t *testing.T) {
	for code, want := range map[string]bool{"ABC234": true, "abc234": false, "ABC23": false, "ABC2340": false, "ABCD1O": false} {
		if ValidCode(code) != want {
			t.Errorf("ValidCode(%q) != %v", code, want)
		}
	}
	for range 100 {
		if c := newCode(); !ValidCode(c) {
			t.Fatalf("generated invalid code %q", c)
		}
	}
}

type hub struct {
	srv *httptest.Server
	reg *Registry
	url string
}

func newHub(t *testing.T, tweak func(*Server)) hub {
	t.Helper()
	reg := NewRegistry(Limits{})
	s := NewServer(reg)
	if tweak != nil {
		tweak(s)
	}
	srv := httptest.NewServer(s)
	t.Cleanup(srv.Close)
	return hub{srv: srv, reg: reg, url: "ws" + strings.TrimPrefix(srv.URL, "http")}
}

func next(t *testing.T, r contract.Room) contract.Signal {
	t.Helper()
	select {
	case s, ok := <-r.Signals():
		if !ok {
			t.Fatal("signals closed")
		}
		return s
	case <-time.After(3 * time.Second):
		t.Fatal("no signal")
	}
	return contract.Signal{}
}

func TestOfferAnswerThroughHub(t *testing.T) {
	testutil.NoLeaks(t)
	h := newHub(t, nil)
	c := New(h.url)
	ctx := context.Background()
	a, err := c.Join(ctx, "", peer('a', "Alpha"))
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := c.Join(ctx, strings.ToLower(a.Code()), peer('b', "Beta"))
	if err != nil {
		t.Fatal(err)
	}
	if s := next(t, a); s.Kind != contract.SignalHello {
		t.Fatalf("a got %+v", s)
	}
	if len(a.Members()) != 2 || len(b.Members()) != 2 {
		t.Fatalf("members %v / %v", a.Members(), b.Members())
	}
	_ = a.Send(ctx, contract.Signal{Kind: contract.SignalOffer, To: peer('b', "").ID, Data: []byte("v=0 offer")})
	if s := next(t, b); s.Kind != contract.SignalOffer || s.From != a.(*clientRoom).self.ID || string(s.Data) != "v=0 offer" {
		t.Fatalf("b got %+v", s)
	}
	_ = b.Send(ctx, contract.Signal{Kind: contract.SignalAnswer, Data: []byte("v=0 answer")})
	if s := next(t, a); s.Kind != contract.SignalAnswer {
		t.Fatalf("a got %+v", s)
	}
	// Clients cannot fake hub messages.
	_ = b.Send(ctx, contract.Signal{Kind: contract.SignalBye})
	_ = b.Send(ctx, contract.Signal{Kind: contract.SignalCandidate, Data: []byte("c")})
	if s := next(t, a); s.Kind != contract.SignalCandidate {
		t.Fatalf("a got %+v; a client-sent bye was relayed", s)
	}

	b.Close()
	if s := next(t, a); s.Kind != contract.SignalBye {
		t.Fatalf("a got %+v", s)
	}
	if len(a.Members()) != 1 {
		t.Fatal("bye did not remove the member")
	}
	a.Close()
	a.Close() // idempotent
	deadline := time.Now().Add(2 * time.Second)
	for {
		if rooms, members := h.reg.Stats(); rooms == 0 && members == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("hub kept state after everyone left")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := c.Join(ctx, a.Code(), peer('c', "C")); !errors.Is(err, ErrNoRoom) {
		t.Fatalf("join closed room: %v", err)
	}
}

func TestHubRejectsBadClients(t *testing.T) {
	testutil.NoLeaks(t)
	h := newHub(t, func(s *Server) { s.Burst, s.Rate, s.MaxMessage = 3, 0.01, 1024 })
	defer http.DefaultClient.CloseIdleConnections()
	for _, q := range []string{"?id=short", "?id=" + strings.Repeat("Z", 32), "?id=" + strings.Repeat("a", 32) + "&avatar=99"} {
		res, err := http.Get(h.srv.URL + q)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: status %d", q, res.StatusCode)
		}
	}

	ctx := context.Background()
	dial := func(c byte) *websocket.Conn {
		conn, _, err := websocket.Dial(ctx, h.url+"?id="+strings.Repeat(string(c), 32), nil)
		if err != nil {
			t.Fatal(err)
		}
		_, _, _ = conn.Read(ctx) // welcome
		return conn
	}
	closedSoon := func(conn *websocket.Conn) bool {
		rctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		for {
			if _, _, err := conn.Read(rctx); err != nil {
				return !errors.Is(err, context.DeadlineExceeded)
			}
		}
	}

	flood := dial('a')
	msg, _ := json.Marshal(contract.Signal{Kind: contract.SignalCandidate})
	for range 10 {
		_ = flood.Write(ctx, websocket.MessageText, msg)
	}
	if !closedSoon(flood) {
		t.Fatal("flooding client not closed")
	}
	flood.CloseNow()

	big := dial('b')
	_ = big.Write(ctx, websocket.MessageText, make([]byte, 4096))
	if !closedSoon(big) {
		t.Fatal("oversized message not refused")
	}
	big.CloseNow()
}

// kick drops one member's connection, as a network failure would.
func kick(r *Registry, code string, id contract.DeviceID) {
	r.mu.Lock()
	m := r.rooms[code].members[id]
	r.mu.Unlock()
	r.Leave(m)
}

func TestClientReconnectsToLiveRoom(t *testing.T) {
	testutil.NoLeaks(t)
	h := newHub(t, nil)
	c := &Client{URL: h.url, Backoff: []time.Duration{10 * time.Millisecond, 20 * time.Millisecond, 50 * time.Millisecond}}
	ctx := context.Background()
	a, err := c.Join(ctx, "", peer('a', "A"))
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	// A member with no socket keeps the room alive across the drop.
	anchor, _, err := h.reg.Join(a.Code(), peer('z', "anchor"))
	if err != nil {
		t.Fatal(err)
	}
	defer h.reg.Leave(anchor)

	kick(h.reg, a.Code(), peer('a', "").ID) // the hub link drops; the room lives on
	// The anchor sees a's bye, then a hello when the client rejoins.
	for s := recvOut(t, anchor); s.Kind != contract.SignalHello || s.From != peer('a', "").ID; s = recvOut(t, anchor) {
	}
	deadline := time.Now().Add(3 * time.Second)
	for a.Send(ctx, contract.Signal{Kind: contract.SignalOffer, Data: []byte("again")}) != nil {
		if time.Now().After(deadline) {
			t.Fatal("client did not reconnect")
		}
		time.Sleep(10 * time.Millisecond)
	}
	for {
		s := recvOut(t, anchor)
		if s.Kind == contract.SignalOffer && string(s.Data) == "again" {
			break
		}
	}
}

func TestClientGivesUpWhenRoomIsGone(t *testing.T) {
	testutil.NoLeaks(t)
	h := newHub(t, nil)
	c := &Client{URL: h.url, Backoff: []time.Duration{10 * time.Millisecond, 10 * time.Millisecond}}
	a, err := c.Join(context.Background(), "", peer('a', "A"))
	if err != nil {
		t.Fatal(err)
	}
	h.reg.now = func() time.Time { return time.Now().Add(time.Hour) }
	h.reg.Sweep() // the room expires on the hub and the link drops
	select {
	case _, ok := <-a.Signals():
		for ok {
			_, ok = <-a.Signals()
		}
	case <-time.After(3 * time.Second):
		t.Fatal("client kept a dead room open")
	}
	a.Close()
}

func TestClientErrors(t *testing.T) {
	testutil.NoLeaks(t)
	h := newHub(t, nil)
	c := New(h.url)
	if _, err := c.Join(context.Background(), "ABC234", peer('a', "A")); !errors.Is(err, ErrNoRoom) {
		t.Fatalf("missing room: %v", err)
	}
	if _, err := New("ws://127.0.0.1:1/ws").Join(context.Background(), "", peer('a', "A")); err == nil {
		t.Fatal("joined a hub that is not there")
	}
	t.Setenv("DROPLINK_SIGNAL_URL", "")
	if URLFromEnv() != DefaultURL {
		t.Fatal(URLFromEnv())
	}
	t.Setenv("DROPLINK_SIGNAL_URL", "wss://hub.example/ws")
	if URLFromEnv() != "wss://hub.example/ws" {
		t.Fatal(URLFromEnv())
	}
}

// Many rooms in parallel: relays stay inside their room.
func TestRoomsAreIsolated(t *testing.T) {
	testutil.NoLeaks(t)
	h := newHub(t, nil)
	c := New(h.url)
	ctx := context.Background()
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			a, err := c.Join(ctx, "", peer("abcdef01"[i], "A"))
			if err != nil {
				t.Error(err)
				return
			}
			defer a.Close()
			b, err := c.Join(ctx, a.Code(), peer("23456789"[i], "B"))
			if err != nil {
				t.Error(err)
				return
			}
			defer b.Close()
			<-a.Signals() // hello
			tag := []byte{byte('0' + i)}
			_ = b.Send(ctx, contract.Signal{Kind: contract.SignalOffer, Data: tag})
			s := <-a.Signals()
			if string(s.Data) != string(tag) {
				t.Errorf("room %d got %q", i, s.Data)
			}
		}()
	}
	wg.Wait()
}

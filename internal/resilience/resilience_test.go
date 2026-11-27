package resilience

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vinayakkam/droplink/internal/contract"
	"github.com/vinayakkam/droplink/internal/engine"
	"github.com/vinayakkam/droplink/internal/files"
	"github.com/vinayakkam/droplink/internal/mock"
	"github.com/vinayakkam/droplink/internal/testutil"
)

var (
	me   = contract.Peer{ID: "me", Name: "Me"}
	them = contract.Peer{ID: "them", Name: "Them", Addr: "10.0.0.2:4000"}
)

// nets is a LAN and a WebRTC mock network; newNets says which routes the
// peer answers on.
type nets struct {
	lan, rtc      *mock.Transport
	farLAN        *mock.Transport
	farRTC        *mock.Transport
	disc          *mock.Discovery
	accepted      chan contract.Route
	stopAccepting func()
}

func newNets(t *testing.T, lanUp, rtcUp bool) *nets {
	t.Helper()
	ln, rn := mock.NewNetwork(), mock.NewNetwork()
	n := &nets{
		lan:      ln.Join(me, contract.RouteLAN),
		rtc:      rn.Join(me, contract.RouteWebRTC),
		disc:     mock.NewDiscovery(),
		accepted: make(chan contract.Route, 8),
	}
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	accept := func(tr *mock.Transport) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				s, err := tr.Accept(ctx)
				if err != nil {
					return
				}
				n.accepted <- tr.Route()
				s.Close()
			}
		}()
	}
	if lanUp {
		n.farLAN = ln.Join(them, contract.RouteLAN)
		accept(n.farLAN)
	}
	if rtcUp {
		n.farRTC = rn.Join(them, contract.RouteWebRTC)
		accept(n.farRTC)
	}
	n.stopAccepting = func() {
		cancel()
		wg.Wait()
	}
	t.Cleanup(func() {
		n.stopAccepting()
		for _, tr := range []*mock.Transport{n.lan, n.rtc, n.farLAN, n.farRTC} {
			if tr != nil {
				tr.Close()
			}
		}
	})
	return n
}

func dial(t *testing.T, r *Router, n *nets) contract.Route {
	t.Helper()
	s, err := r.Dial(context.Background(), contract.Peer{ID: them.ID})
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	select {
	case rt := <-n.accepted:
		return rt
	case <-time.After(2 * time.Second):
		t.Fatal("nobody accepted")
	}
	return ""
}

func TestRouterPrefersLAN(t *testing.T) {
	testutil.NoLeaks(t)
	n := newNets(t, true, true)
	n.disc.Add(them)
	if rt := dial(t, NewRouter(n.disc, n.lan, n.rtc), n); rt != contract.RouteLAN {
		t.Fatalf("used %s", rt)
	}
}

func TestRouterUsesWebRTCWhenNotOnLAN(t *testing.T) {
	testutil.NoLeaks(t)
	n := newNets(t, true, true)
	r := NewRouter(n.disc, n.lan, n.rtc) // discovery never saw them
	if rt := dial(t, r, n); rt != contract.RouteWebRTC {
		t.Fatalf("used %s", rt)
	}

	// Seen, but 10 s ago: stale, still WebRTC.
	now := time.Now()
	r.now = func() time.Time { return now.Add(10 * time.Second) }
	n.disc.Add(them)
	if rt := dial(t, r, n); rt != contract.RouteWebRTC {
		t.Fatalf("stale LAN sighting used: %s", rt)
	}
}

func TestRouterFallsBackAndRemembers(t *testing.T) {
	testutil.NoLeaks(t)
	n := newNets(t, false, true) // seen on the LAN, but only WebRTC answers
	n.disc.Add(them)
	r := NewRouter(n.disc, n.lan, n.rtc)
	if rt := dial(t, r, n); rt != contract.RouteWebRTC {
		t.Fatalf("used %s", rt)
	}
	r.mu.Lock()
	pref := r.last[them.ID]
	r.mu.Unlock()
	if pref != contract.RouteWebRTC {
		t.Fatalf("remembered %q", pref)
	}
	if routes := r.plan(contract.Peer{ID: them.ID}); routes[0].t.Route() != contract.RouteWebRTC {
		t.Fatal("last working route not tried first")
	}
	r.Reset()
	if routes := r.plan(contract.Peer{ID: them.ID}); routes[0].t.Route() != contract.RouteLAN {
		t.Fatal("Reset kept the remembered route")
	}
}

func TestRouterNoRoute(t *testing.T) {
	r := NewRouter(mock.NewDiscovery(), nil, nil)
	if _, err := r.Dial(context.Background(), them); !errors.Is(err, ErrNoRoute) {
		t.Fatalf("err = %v", err)
	}
	n := newNets(t, false, false)
	r = NewRouter(n.disc, n.lan, n.rtc)
	if _, err := r.Dial(context.Background(), them); err == nil {
		t.Fatal("dialed a peer nobody can reach")
	}
}

func TestDelayGrowsCapsAndJitters(t *testing.T) {
	p := Policy{}
	for n, want := range map[int]time.Duration{1: 500 * time.Millisecond, 2: time.Second, 3: 2 * time.Second, 5: 8 * time.Second, 9: 8 * time.Second, 60: 8 * time.Second} {
		for range 50 {
			d := p.Delay(n)
			if d < time.Duration(float64(want)*0.8) || d > time.Duration(float64(want)*1.2) {
				t.Fatalf("Delay(%d) = %v, want %v ±20%%", n, d, want)
			}
		}
	}
}

func TestRetryable(t *testing.T) {
	op := &net.OpError{Op: "dial", Err: errors.New("refused")}
	for err, want := range map[error]bool{
		nil:                                      false,
		io.EOF:                                   true,
		io.ErrUnexpectedEOF:                      true,
		net.ErrClosed:                            true,
		op:                                       true,
		fmt.Errorf("send: %w", io.ErrClosedPipe): true,
		engine.ErrCorrupt:                        true,
		engine.ErrRejected:                       false,
		engine.ErrCanceled:                       false,
		engine.ErrProtocol:                       false,
		engine.ErrRemote:                         false,
		context.Canceled:                         false,
		os.ErrPermission:                         false,
		errors.New("something else"):             false,
	} {
		if got := Retryable(err); got != want {
			t.Errorf("Retryable(%v) = %v, want %v", err, got, want)
		}
	}
}

func instant() Policy {
	return Policy{sleep: func(ctx context.Context, _ time.Duration) error { return ctx.Err() }}
}

func TestDoStopsOnFinalErrors(t *testing.T) {
	var calls int
	err := instant().Do(context.Background(), func(context.Context, int) error { calls++; return engine.ErrRejected })
	if !errors.Is(err, engine.ErrRejected) || calls != 1 {
		t.Fatalf("calls %d, err %v", calls, err)
	}
	calls = 0
	err = instant().Do(context.Background(), func(_ context.Context, n int) error {
		calls++
		if n < 3 {
			return io.EOF
		}
		return nil
	})
	if err != nil || calls != 3 {
		t.Fatalf("calls %d, err %v", calls, err)
	}
	calls = 0
	err = instant().Do(context.Background(), func(context.Context, int) error { calls++; return io.EOF })
	if !errors.Is(err, io.EOF) || calls != 6 {
		t.Fatalf("calls %d (want 6), err %v", calls, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	calls = 0
	err = instant().Do(ctx, func(context.Context, int) error { calls++; cancel(); return io.EOF })
	if calls != 1 || err == nil {
		t.Fatalf("kept retrying after cancel: calls %d", calls)
	}
}

// cutRouter's first stream dies after cutAt bytes, like Wi-Fi dropping.
type cutRouter struct {
	contract.Router
	cutAt  int64
	dials  atomic.Int32
	cutOff atomic.Int64 // bytes the first stream carried
}

type cutStream struct {
	contract.Stream
	left int64
	r    *cutRouter
}

func (c *cutStream) Write(p []byte) (int, error) {
	if int64(len(p)) > c.left {
		c.Stream.Close()
		return 0, io.ErrClosedPipe
	}
	c.left -= int64(len(p))
	c.r.cutOff.Add(int64(len(p)))
	return c.Stream.Write(p)
}

func (c *cutRouter) Dial(ctx context.Context, to contract.Peer) (contract.Stream, error) {
	s, err := c.Router.Dial(ctx, to)
	if err != nil || c.dials.Add(1) > 1 {
		return s, err
	}
	return &cutStream{Stream: s, left: c.cutAt, r: c}, nil
}

type pattern struct{ size, pos int64 }

func (p *pattern) Read(b []byte) (int, error) {
	if p.pos >= p.size {
		return 0, io.EOF
	}
	n := int(min(int64(len(b)), p.size-p.pos))
	for i := range n {
		b[i] = byte(uint64(p.pos+int64(i)) * 0x9E3779B97F4A7C15 >> 56)
	}
	p.pos += int64(n)
	return n, nil
}
func (p *pattern) Seek(off int64, _ int) (int64, error) { p.pos = off; return off, nil }
func (p *pattern) Close() error                         { return nil }

type src struct{}

func (src) Open(f contract.FileMeta) (io.ReadSeekCloser, error) { return &pattern{size: f.Size}, nil }

func TestDroppedTransferResumes(t *testing.T) {
	testutil.NoLeaks(t)
	ln := mock.NewNetwork()
	mine, theirs := ln.Join(me, contract.RouteLAN), ln.Join(them, contract.RouteLAN)
	defer mine.Close()
	defer theirs.Close()
	disc := mock.NewDiscovery()
	disc.Add(them)

	out := t.TempDir()
	sink, err := files.NewSink(out)
	if err != nil {
		t.Fatal(err)
	}
	e := engine.New(64 << 10)
	const size = 2 << 20

	// The receiver accepts every attempt, like the manager does.
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	received := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			s, err := theirs.Accept(ctx)
			if err != nil {
				return
			}
			if _, err := e.Receive(ctx, s, func(contract.Offer) bool { return true }, sink, nil); err == nil {
				close(received)
				return
			}
		}
	}()

	r := &cutRouter{Router: NewRouter(disc, mine, nil), cutAt: 700 << 10}
	o := contract.Offer{ID: "resume", Files: []contract.FileMeta{{Path: "map.bin", Size: size}}}
	// Each attempt reports "offered" first; the second attempt's first
	// "active" update says where it resumed.
	var attempts int
	var resumedFrom int64 = -1
	var mu sync.Mutex
	err = Send(ctx, instant(), r, e, them, o, src{}, func(p contract.Progress) {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case p.State == contract.StateOffered:
			attempts++
		case p.State == contract.StateActive && attempts == 2 && resumedFrom < 0:
			resumedFrom = p.Done
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	<-received
	cancel()
	wg.Wait()

	if r.dials.Load() != 2 {
		t.Fatalf("%d dials, want 2 (one cut, one resumed)", r.dials.Load())
	}
	got, _ := os.ReadFile(filepath.Join(out, "map.bin"))
	want, _ := io.ReadAll(&pattern{size: size})
	if !bytes.Equal(got, want) {
		t.Fatal("resumed file differs from the original")
	}
	mu.Lock()
	defer mu.Unlock()
	if resumedFrom < 256<<10 {
		t.Fatalf("second attempt resumed from %d; it should skip what the first stored", resumedFrom)
	}
}

func TestWatcher(t *testing.T) {
	testutil.NoLeaks(t)
	var net atomic.Int32
	changes := make(chan struct{}, 4)
	w := &Watcher{Every: 10 * time.Millisecond, OnChange: func() { changes <- struct{}{} },
		fingerprint: func() string { return fmt.Sprint(net.Load()) }}
	w.Start()
	select {
	case <-changes:
		t.Fatal("change reported without one")
	case <-time.After(50 * time.Millisecond):
	}
	net.Add(1)
	select {
	case <-changes:
	case <-time.After(time.Second):
		t.Fatal("change not reported")
	}
	w.Close()
	w.Close()
	(&Watcher{}).Close() // never started
	if a, b := Fingerprint(), Fingerprint(); a != b {
		t.Fatal("fingerprint unstable")
	}
}

func TestWatchNetworkResetsRouter(t *testing.T) {
	testutil.NoLeaks(t)
	r := NewRouter(mock.NewDiscovery(), nil, nil)
	r.last[them.ID] = contract.RouteWebRTC
	announced := make(chan struct{}, 1)
	r.WatchNetwork(time.Hour, func() { announced <- struct{}{} })
	r.watch.OnChange() // as if the network changed
	<-announced
	if len(r.last) != 0 {
		t.Fatal("route cache kept after a network change")
	}
	r.Close()
	if err := NewRouter(nil, nil, nil).Close(); err != nil {
		t.Fatal(err)
	}
}

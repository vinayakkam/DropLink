package discovery

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vinayakkam/droplink/internal/contract"
	"github.com/vinayakkam/droplink/internal/testutil"
)

const (
	idA = contract.DeviceID("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	idB = contract.DeviceID("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
)

func TestBeaconRoundTrip(t *testing.T) {
	b := Beacon{ID: idA, Name: "Nova ☄", Avatar: 7, Port: 51234, Nonce: 42}
	pkt, err := b.Encode(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(pkt) > MaxBeacon {
		t.Fatalf("%d bytes", len(pkt))
	}
	got, err := Decode(pkt)
	if err != nil || got != b {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func TestBeaconTruncatesOnRuneBoundary(t *testing.T) {
	long := strings.Repeat("é", 40) // 80 bytes
	pkt, _ := Beacon{ID: idA, Name: long}.Encode(nil)
	got, err := Decode(pkt)
	if err != nil || len(got.Name) > MaxName || !strings.HasPrefix(long, got.Name) {
		t.Fatalf("name %q (%d bytes), %v", got.Name, len(got.Name), err)
	}
}

func TestDecodeRejects(t *testing.T) {
	good, _ := Beacon{ID: idA, Name: "ok"}.Encode(nil)
	mut := func(f func([]byte) []byte) []byte { return f(append([]byte(nil), good...)) }
	cases := map[string][]byte{
		"empty":       nil,
		"short":       good[:10],
		"magic":       mut(func(p []byte) []byte { p[0] = 'X'; return p }),
		"version":     mut(func(p []byte) []byte { p[4] = 9; return p }),
		"avatar":      mut(func(p []byte) []byte { p[5] = 16; return p }),
		"id char":     mut(func(p []byte) []byte { p[16] = 'Z'; return p }),
		"name length": mut(func(p []byte) []byte { p[48] = 5; return p }),
		"trailing":    append(append([]byte(nil), good...), 'x'),
		"bad utf8":    mut(func(p []byte) []byte { p[len(p)-1] = 0xff; return p }),
		"control":     mut(func(p []byte) []byte { p[len(p)-1] = '\n'; return p }),
		"oversized":   make([]byte, MaxBeacon+1),
	}
	for name, pkt := range cases {
		if _, err := Decode(pkt); !errors.Is(err, ErrBeacon) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	if _, err := (Beacon{ID: "short"}).Encode(nil); err == nil {
		t.Error("encoded a bad ID")
	}
}

func FuzzDecode(f *testing.F) {
	good, _ := Beacon{ID: idA, Name: "seed", Port: 1}.Encode(nil)
	f.Add(good)
	f.Add([]byte("DLNK"))
	f.Fuzz(func(t *testing.T, p []byte) {
		b, err := Decode(p)
		if err != nil {
			return
		}
		again, err := b.Encode(nil)
		if err != nil {
			t.Fatalf("decoded beacon does not re-encode: %v", err)
		}
		if string(again) != string(p) {
			t.Fatalf("re-encode differs")
		}
	})
}

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func next(t *testing.T, ch <-chan contract.PeerEvent) contract.PeerEvent {
	t.Helper()
	select {
	case ev := <-ch:
		return ev
	case <-time.After(3 * time.Second):
		t.Fatal("no event")
	}
	return contract.PeerEvent{}
}

func none(t *testing.T, ch <-chan contract.PeerEvent) {
	t.Helper()
	select {
	case ev := <-ch:
		t.Fatalf("unexpected event %+v", ev)
	case <-time.After(20 * time.Millisecond):
	}
}

func TestTableEvents(t *testing.T) {
	c := &clock{t: time.Unix(1000, 0)}
	tb := newTable(idA, 7*time.Second)
	tb.now = c.now
	ev, cancel := tb.subscribe()
	defer cancel()
	ip := net.IPv4(192, 168, 1, 20)

	tb.observe(Beacon{ID: idA, Name: "me"}, ip) // self: ignored
	none(t, ev)

	tb.observe(Beacon{ID: idB, Name: "Vega", Port: 4000, Nonce: 1}, ip)
	if e := next(t, ev); e.Kind != contract.PeerJoined || e.Peer.Addr != "192.168.1.20:4000" || e.Peer.Route != contract.RouteLAN {
		t.Fatalf("joined = %+v", e)
	}
	c.add(time.Second)
	tb.observe(Beacon{ID: idB, Name: "Vega", Port: 4000, Nonce: 1}, ip) // same: refresh only
	none(t, ev)
	tb.observe(Beacon{ID: idB, Name: "Vega 2", Port: 4000, Nonce: 1}, ip)
	if e := next(t, ev); e.Kind != contract.PeerUpdated || e.Peer.Name != "Vega 2" {
		t.Fatalf("rename = %+v", e)
	}
	tb.observe(Beacon{ID: idB, Name: "Vega 2", Port: 4000, Nonce: 2}, ip) // restarted
	if e := next(t, ev); e.Kind != contract.PeerUpdated {
		t.Fatalf("restart = %+v", e)
	}

	c.add(6 * time.Second)
	tb.expire()
	none(t, ev) // 6 s < TTL
	c.add(2 * time.Second)
	tb.expire()
	if e := next(t, ev); e.Kind != contract.PeerLeft || e.Peer.ID != idB {
		t.Fatalf("left = %+v", e)
	}
	if len(tb.list()) != 0 {
		t.Fatal("expired peer still listed")
	}
}

func TestTablePortZeroHasNoAddr(t *testing.T) {
	tb := newTable(idA, time.Second)
	tb.observe(Beacon{ID: idB, Name: "x"}, net.IPv4(10, 0, 0, 1))
	if p := tb.list()[0]; p.Addr != "" {
		t.Fatalf("addr %q for port 0", p.Addr)
	}
}

func TestSlowSubscriberNeverBlocks(t *testing.T) {
	tb := newTable(idA, time.Second)
	_, cancel := tb.subscribe() // never read
	done := make(chan struct{})
	go func() {
		for i := range 500 {
			id := contract.DeviceID(strings.Repeat(string("0123456789abcdef"[i%16]), 31) + string("0123456789abcdef"[i/16%16]))
			tb.observe(Beacon{ID: id, Name: "p"}, net.IPv4(10, 0, 0, 1))
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("observe blocked on a slow subscriber")
	}
	cancel()
	cancel()
}

func TestPeersSorted(t *testing.T) {
	tb := newTable(idA, time.Second)
	ip := net.IPv4(10, 0, 0, 1)
	tb.observe(Beacon{ID: idB, Name: "zeta"}, ip)
	tb.observe(Beacon{ID: "cccccccccccccccccccccccccccccccc", Name: "Alpha"}, ip)
	tb.observe(Beacon{ID: "dddddddddddddddddddddddddddddddd", Name: "beta"}, ip)
	var names []string
	for _, p := range tb.list() {
		names = append(names, p.Name)
	}
	if strings.Join(names, ",") != "Alpha,beta,zeta" {
		t.Fatalf("order %v", names)
	}
}

// freeUDP returns a free loopback UDP port.
func freeUDP(t *testing.T) *net.UDPAddr {
	t.Helper()
	c, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	return c.LocalAddr().(*net.UDPAddr)
}

func loopbackPair(t *testing.T, ttl time.Duration) (*Discovery, *Discovery) {
	t.Helper()
	pa, pb := freeUDP(t), freeUDP(t)
	opts := func(listen, target *net.UDPAddr) Options {
		return Options{ListenAddr: listen.String(), Targets: []*net.UDPAddr{target}, Every: 30 * time.Millisecond, TTL: ttl}
	}
	a := New(contract.Peer{ID: idA, Name: "Alpha", Avatar: 3}, 7001, opts(pa, pb))
	b := New(contract.Peer{ID: idB, Name: "Beta"}, 7002, opts(pb, pa))
	return a, b
}

func TestTwoDevicesOnLoopback(t *testing.T) {
	testutil.NoLeaks(t)
	a, b := loopbackPair(t, 300*time.Millisecond)
	evA, cancelA := a.Subscribe()
	defer cancelA()

	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	ctxB, stopB := context.WithCancel(ctx)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); _ = a.Run(ctx) }()
	go func() { defer wg.Done(); _ = b.Run(ctxB) }()

	e := next(t, evA)
	if e.Kind != contract.PeerJoined || e.Peer.ID != idB || e.Peer.Addr != "127.0.0.1:7002" {
		t.Fatalf("a saw %+v", e)
	}
	b.SetProfile("Beta Prime", 9)
	for e = next(t, evA); e.Kind != contract.PeerUpdated; e = next(t, evA) {
	}
	if e.Peer.Name != "Beta Prime" || e.Peer.Avatar != 9 {
		t.Fatalf("update = %+v", e)
	}
	b.SetPort(7777)
	for e = next(t, evA); e.Peer.Addr != "127.0.0.1:7777"; e = next(t, evA) {
	}

	stopB() // b goes quiet
	for e = next(t, evA); e.Kind != contract.PeerLeft; e = next(t, evA) {
	}
	if len(a.Peers()) != 0 {
		t.Fatal("left peer still listed")
	}
	stop()
	wg.Wait()
}

func TestNetworkChangeAnnouncesAtOnce(t *testing.T) {
	testutil.NoLeaks(t)
	sink, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer sink.Close()
	var changes atomic.Int32
	d := New(contract.Peer{ID: idA, Name: "A"}, 1, Options{
		ListenAddr: freeUDP(t).String(),
		Targets:    []*net.UDPAddr{sink.LocalAddr().(*net.UDPAddr)},
		Every:      time.Hour, // only the first beacon and change-triggered ones
		Rescan:     20 * time.Millisecond,
		fingerprint: func() string {
			return string(rune('a' + changes.Load()))
		},
	})
	ctx, stop := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = d.Run(ctx); close(done) }()

	buf := make([]byte, MaxBeacon)
	read := func() bool {
		_ = sink.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
		_, _, err := sink.ReadFromUDP(buf)
		return err == nil
	}
	if !read() {
		t.Fatal("no first beacon")
	}
	if read() {
		t.Fatal("beacon without a change or tick")
	}
	changes.Add(1) // "Wi-Fi switched"
	if !read() {
		t.Fatal("no beacon after the network changed")
	}
	stop()
	<-done
}

func TestRealMulticast(t *testing.T) {
	if testing.Short() {
		t.Skip("uses the real network")
	}
	testutil.NoLeaks(t)
	group := &net.UDPAddr{IP: DefaultGroup.IP, Port: freeUDP(t).Port}
	a := New(contract.Peer{ID: idA, Name: "A"}, 1, Options{Group: group, Every: 50 * time.Millisecond, NoBroadcast: true})
	b := New(contract.Peer{ID: idB, Name: "B"}, 2, Options{Group: group, Every: 50 * time.Millisecond, NoBroadcast: true})
	ev, cancel := a.Subscribe()
	defer cancel()
	ctx, stop := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	for _, d := range []*Discovery{a, b} {
		wg.Add(1)
		go func() { defer wg.Done(); _ = d.Run(ctx) }()
	}
	defer func() { stop(); wg.Wait() }()
	select {
	case e := <-ev:
		if e.Peer.ID != idB {
			t.Fatalf("saw %+v", e)
		}
	case <-time.After(time.Second):
		t.Skip("multicast loopback not available on this machine/network")
	}
}

func TestPortFromEnv(t *testing.T) {
	t.Setenv("DROPLINK_LAN_PORT", "4567")
	if PortFromEnv() != 4567 {
		t.Fatal(PortFromEnv())
	}
	t.Setenv("DROPLINK_LAN_PORT", "99999")
	if PortFromEnv() != 0 {
		t.Fatal("out-of-range port accepted")
	}
}

func TestBroadcastAddrs(t *testing.T) {
	for _, ip := range broadcastAddrs() {
		if ip.To4() == nil || ip.IsLoopback() {
			t.Fatalf("bad broadcast address %v", ip)
		}
	}
}

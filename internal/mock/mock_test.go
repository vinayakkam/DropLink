package mock

import (
	"context"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"github.com/vinayakkam/droplink/internal/contract"
	"github.com/vinayakkam/droplink/internal/testutil"
)

func peer(id, name string) contract.Peer {
	return contract.Peer{ID: contract.DeviceID(id), Name: name}
}

func ctx(t *testing.T) context.Context {
	c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return c
}

func TestDiscoveryEvents(t *testing.T) {
	testutil.NoLeaks(t)
	d := NewDiscovery(peer("b", "Beta"))
	ev, cancel := d.Subscribe()
	defer cancel()

	d.Add(peer("a", "Alpha"))
	d.Add(peer("a", "Alpha 2"))
	d.Remove("a")
	d.Remove("missing") // no event

	for _, want := range []contract.PeerEventKind{contract.PeerJoined, contract.PeerUpdated, contract.PeerLeft} {
		select {
		case e := <-ev:
			if e.Kind != want || e.Peer.ID != "a" {
				t.Fatalf("event = %+v, want %s for a", e, want)
			}
		case <-time.After(time.Second):
			t.Fatalf("no %s event", want)
		}
	}
	if ps := d.Peers(); len(ps) != 1 || ps[0].Name != "Beta" {
		t.Fatalf("peers = %+v", ps)
	}
}

func TestDiscoverySlowSubscriberNeverBlocks(t *testing.T) {
	d := NewDiscovery()
	_, cancel := d.Subscribe() // never read
	done := make(chan struct{})
	go func() {
		for i := range 100 {
			d.Add(peer(string(rune('a'+i%26))+"x", "p"))
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Add blocked on a slow subscriber")
	}
	cancel()
	cancel() // idempotent
}

func TestDiscoveryPeersSorted(t *testing.T) {
	d := NewDiscovery(peer("1", "zeta"), peer("2", "alpha"), peer("3", "mid"))
	ps := d.Peers()
	if ps[0].Name != "alpha" || ps[1].Name != "mid" || ps[2].Name != "zeta" {
		t.Fatalf("not sorted: %+v", ps)
	}
}

func TestCryptoHandshakeAgreesOnSAS(t *testing.T) {
	testutil.NoLeaks(t)
	a, b := NewCrypto("aaaa"), NewCrypto("bbbb")
	x, y := net.Pipe()
	defer x.Close()
	defer y.Close()

	type res struct {
		c   contract.SecureConn
		err error
	}
	out := make(chan res, 1)
	go func() { c, err := b.Secure(ctx(t), y, false); out <- res{c, err} }()
	ca, err := a.Secure(ctx(t), x, true)
	if err != nil {
		t.Fatal(err)
	}
	rb := <-out
	if rb.err != nil {
		t.Fatal(rb.err)
	}
	if ca.RemoteID() != "bbbb" || rb.c.RemoteID() != "aaaa" {
		t.Fatalf("remote ids: %q %q", ca.RemoteID(), rb.c.RemoteID())
	}
	if ca.SAS() != rb.c.SAS() {
		t.Fatalf("SAS differ: %v vs %v", ca.SAS(), rb.c.SAS())
	}
	for _, g := range ca.SAS() {
		if g > 63 {
			t.Fatalf("glyph %d out of range", g)
		}
	}
	// Data still flows after the handshake.
	go func() { _, _ = ca.Write([]byte("ping")) }()
	buf := make([]byte, 4)
	if _, err := io.ReadFull(rb.c, buf); err != nil || string(buf) != "ping" {
		t.Fatalf("read %q, %v", buf, err)
	}
}

func TestSASSymmetricAndDistinct(t *testing.T) {
	if SASFor("a", "b") != SASFor("b", "a") {
		t.Fatal("SAS depends on order")
	}
	if SASFor("a", "b") == SASFor("a", "c") {
		t.Fatal("different pairs gave the same SAS (unlikely unless broken)")
	}
}

func TestCryptoTrust(t *testing.T) {
	c := NewCrypto("me")
	if c.IsTrusted("x") {
		t.Fatal("trusted before Trust")
	}
	_ = c.Trust(contract.TrustedDevice{ID: "y", Name: "Y"})
	_ = c.Trust(contract.TrustedDevice{ID: "x", Name: "X"})
	if !c.IsTrusted("x") || len(c.Trusted()) != 2 || c.Trusted()[0].ID != "x" {
		t.Fatalf("trusted = %+v", c.Trusted())
	}
	_ = c.Untrust("x")
	if c.IsTrusted("x") {
		t.Fatal("still trusted after Untrust")
	}
}

func TestTransportDialAccept(t *testing.T) {
	testutil.NoLeaks(t)
	n := NewNetwork()
	a := n.Join(peer("a", "A"), contract.RouteLAN)
	b := n.Join(peer("b", "B"), contract.RouteLAN)
	defer a.Close()
	defer b.Close()

	got := make(chan contract.Stream, 1)
	go func() {
		s, err := b.Accept(ctx(t))
		if err != nil {
			t.Error(err)
		}
		got <- s
	}()
	sa, err := a.Dial(ctx(t), peer("b", "B"))
	if err != nil {
		t.Fatal(err)
	}
	sb := <-got
	if sa.Remote().ID != "b" || sb.Remote().ID != "a" {
		t.Fatalf("remotes: %q %q", sa.Remote().ID, sb.Remote().ID)
	}
	go func() { _, _ = sa.Write([]byte("cargo")); sa.Close() }()
	data, err := io.ReadAll(sb)
	if err != nil || string(data) != "cargo" {
		t.Fatalf("got %q, %v", data, err)
	}
	sb.Close()
}

func TestTransportErrors(t *testing.T) {
	testutil.NoLeaks(t)
	n := NewNetwork()
	a := n.Join(peer("a", "A"), contract.RouteLAN)
	if a.Route() != contract.RouteLAN {
		t.Fatal("route")
	}
	if _, err := a.Dial(ctx(t), peer("ghost", "G")); err == nil {
		t.Fatal("dial to unknown peer succeeded")
	}

	errc := make(chan error, 1)
	go func() { _, err := a.Accept(context.Background()); errc <- err }()
	time.Sleep(20 * time.Millisecond)
	a.Close()
	a.Close() // idempotent
	if err := <-errc; !errors.Is(err, ErrClosed) {
		t.Fatalf("Accept after Close = %v", err)
	}

	short, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	b := n.Join(peer("b", "B"), contract.RouteLAN)
	c := n.Join(peer("c", "C"), contract.RouteLAN)
	defer b.Close()
	defer c.Close()
	if _, err := b.Dial(short, peer("c", "C")); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("dial with nobody accepting = %v", err)
	}
}

func TestPipe(t *testing.T) {
	x, y := Pipe(peer("a", "A"), peer("b", "B"))
	defer x.Close()
	defer y.Close()
	if x.Remote().ID != "b" || y.Remote().ID != "a" {
		t.Fatal("pipe remotes swapped")
	}
}

func recv(t *testing.T, r contract.Room) contract.Signal {
	t.Helper()
	select {
	case s := <-r.Signals():
		return s
	case <-time.After(time.Second):
		t.Fatal("no signal")
	}
	return contract.Signal{}
}

func TestHubRelay(t *testing.T) {
	h := NewHub()
	a, err := h.Join(ctx(t), "", peer("a", "A"))
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Code()) != 6 {
		t.Fatalf("code %q", a.Code())
	}
	b, _ := h.Join(ctx(t), a.Code(), peer("b", "B"))
	c, _ := h.Join(ctx(t), a.Code(), peer("c", "C"))
	if len(a.Members()) != 3 {
		t.Fatalf("members = %d", len(a.Members()))
	}
	if s := recv(t, a); s.Kind != contract.SignalHello || s.From != "b" {
		t.Fatalf("a got %+v", s)
	}
	recv(t, a) // hello from c
	recv(t, b) // hello from c

	// Directed: only b gets it; From is set by the hub, not trusted from the sender.
	_ = a.Send(ctx(t), contract.Signal{Kind: contract.SignalOffer, To: "b", From: "forged", Data: []byte("sdp")})
	if s := recv(t, b); s.From != "a" || string(s.Data) != "sdp" {
		t.Fatalf("b got %+v", s)
	}
	select {
	case s := <-c.Signals():
		t.Fatalf("c got a directed signal: %+v", s)
	case <-time.After(30 * time.Millisecond):
	}

	_ = c.Close()
	if s := recv(t, a); s.Kind != contract.SignalBye || s.From != "c" {
		t.Fatalf("a got %+v, want bye", s)
	}
	if err := c.Send(ctx(t), contract.Signal{Kind: contract.SignalOffer}); !errors.Is(err, ErrNoRoom) {
		t.Fatalf("send after close = %v", err)
	}
	if _, ok := <-c.Signals(); ok {
		t.Fatal("closed room channel still open")
	}

	_ = a.Close()
	_ = b.Close()
	if _, err := h.Join(ctx(t), a.Code(), peer("d", "D")); !errors.Is(err, ErrNoRoom) {
		t.Fatalf("empty room was kept: %v", err)
	}
}

func TestRouterFallsBack(t *testing.T) {
	testutil.NoLeaks(t)
	n := NewNetwork()
	lan := n.Join(peer("a", "A"), contract.RouteLAN)
	far := n.Join(peer("b", "B"), contract.RouteLAN)
	defer lan.Close()
	defer far.Close()

	dead := NewNetwork().Join(peer("a", "A"), contract.RouteWebRTC) // knows nobody
	defer dead.Close()
	r := &Router{Transports: []contract.Transport{dead, lan}}

	go func() {
		s, err := far.Accept(ctx(t))
		if err == nil {
			s.Close()
		}
	}()
	s, err := r.Dial(ctx(t), peer("b", "B"))
	if err != nil {
		t.Fatal(err)
	}
	if s.Remote().Route != contract.RouteLAN {
		t.Fatalf("route = %q, want lan", s.Remote().Route)
	}
	s.Close()

	if _, err := (&Router{}).Dial(ctx(t), peer("b", "B")); err == nil {
		t.Fatal("empty router dialed")
	}
}

func TestHistoryCapAndOrder(t *testing.T) {
	h := &History{Max: 3}
	for i := range 5 {
		_ = h.Append(contract.Record{ID: contract.TransferID(rune('0' + i))})
	}
	recs, _ := h.List(0)
	if len(recs) != 3 || recs[0].ID != "4" || recs[2].ID != "2" {
		t.Fatalf("records = %+v", recs)
	}
	if recs, _ := h.List(1); len(recs) != 1 || recs[0].ID != "4" {
		t.Fatalf("limit ignored: %+v", recs)
	}
	_ = h.Clear()
	if recs, _ := h.List(0); len(recs) != 0 {
		t.Fatal("not cleared")
	}
}

func TestOfferTotalSize(t *testing.T) {
	o := contract.Offer{Files: []contract.FileMeta{{Size: 3}, {Size: 4}}}
	if o.TotalSize() != 7 {
		t.Fatal(o.TotalSize())
	}
}

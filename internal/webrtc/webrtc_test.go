package webrtc

import (
	"context"
	"errors"
	"io"

	"strings"
	"sync"
	"testing"
	"time"

	pion "github.com/pion/webrtc/v4"

	"github.com/vinayakkam/droplink/internal/contract"
	"github.com/vinayakkam/droplink/internal/engine"
	"github.com/vinayakkam/droplink/internal/mock"
	"github.com/vinayakkam/droplink/internal/testutil"
)

func id(c byte) contract.DeviceID { return contract.DeviceID(strings.Repeat(string(c), 32)) }

func opts() Options {
	return Options{ICEServers: []pion.ICEServer{}, IncludeLoopback: true, ConnectTimeout: 5 * time.Second}
}

// pair joins two transports to one room on an in-process hub.
func pair(t *testing.T) (*Transport, *Transport) {
	t.Helper()
	hub := mock.NewHub()
	a := New(contract.Peer{ID: id('a'), Name: "Alpha"}, hub, opts())
	b := New(contract.Peer{ID: id('b'), Name: "Beta"}, hub, opts())
	t.Cleanup(func() { a.Close(); b.Close() })
	code, err := a.JoinRoom(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.JoinRoom(context.Background(), code); err != nil {
		t.Fatal(err)
	}
	return a, b
}

func connect(t *testing.T, a, b *Transport) (contract.Stream, contract.Stream) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	sa, err := a.Dial(ctx, contract.Peer{ID: b.self.ID})
	if err != nil {
		t.Fatal(err)
	}
	sb, err := b.Accept(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return sa, sb
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

type sink struct{}
type nopWC struct{ io.Writer }

func (nopWC) Close() error                                         { return nil }
func (sink) Open(contract.FileMeta) (io.WriteCloser, int64, error) { return nopWC{io.Discard}, 0, nil }
func (sink) Commit(contract.FileMeta) error                        { return nil }
func (sink) Abort(contract.FileMeta, error) error                  { return nil }

func TestEngineMoves32MiB(t *testing.T) {
	testutil.NoLeaks(t)
	a, b := pair(t)
	sa, sb := connect(t, a, b)
	if sb.Remote().ID != a.self.ID || sb.Remote().Name != "Alpha" || sb.Remote().Route != contract.RouteWebRTC {
		t.Fatalf("accepted from %+v", sb.Remote())
	}
	if sa.(*stream).SAS() != sb.(*stream).SAS() {
		t.Fatal("SAS differs between the ends")
	}

	e := engine.New(64 << 10)
	o := contract.Offer{ID: "rtc", Files: []contract.FileMeta{{Path: "big.bin", Size: 32 << 20}}}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	sent := make(chan error, 1)
	go func() { sent <- e.Send(ctx, sa, o, src{}, nil) }()
	if _, err := e.Receive(ctx, sb, func(contract.Offer) bool { return true }, sink{}, nil); err != nil {
		t.Fatal(err)
	}
	if err := <-sent; err != nil {
		t.Fatal(err)
	}
	if peak := sa.(*stream).peak.Load(); peak > highWater+maxMessage {
		t.Fatalf("send buffer peaked at %d bytes; backpressure should cap it at %d", peak, highWater+maxMessage)
	}
	a.Close()
	b.Close()
}

func TestDialNeedsARoomAndAMember(t *testing.T) {
	testutil.NoLeaks(t)
	hub := mock.NewHub()
	o := opts()
	o.ConnectTimeout = 300 * time.Millisecond
	a := New(contract.Peer{ID: id('a')}, hub, o)
	defer a.Close()
	if _, err := a.Dial(context.Background(), contract.Peer{ID: id('b')}); !errors.Is(err, ErrNoRoom) {
		t.Fatalf("dial without a room: %v", err)
	}
	if _, err := a.JoinRoom(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if _, err := a.Dial(context.Background(), contract.Peer{ID: id('c')}); !errors.Is(err, errTimeout) {
		t.Fatalf("dial to nobody: %v", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("timeout not applied")
	}
	if len(a.RoomMembers()) != 1 {
		t.Fatalf("members %v", a.RoomMembers())
	}
}

func TestRelayFallbackAfterTimeout(t *testing.T) {
	testutil.NoLeaks(t)
	for _, tc := range []struct {
		name string
		ice  []pion.ICEServer
		want []pion.ICETransportPolicy
	}{
		{"with TURN", []pion.ICEServer{{URLs: []string{"turn:127.0.0.1:9?transport=udp"}, Username: "u", Credential: "p"}},
			[]pion.ICETransportPolicy{pion.ICETransportPolicyAll, pion.ICETransportPolicyRelay}},
		{"STUN only", []pion.ICEServer{}, []pion.ICETransportPolicy{pion.ICETransportPolicyAll}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var mu sync.Mutex
			var got []pion.ICETransportPolicy
			o := opts()
			o.ICEServers, o.ConnectTimeout = tc.ice, 200*time.Millisecond
			o.onAttempt = func(p pion.ICETransportPolicy) { mu.Lock(); got = append(got, p); mu.Unlock() }
			a := New(contract.Peer{ID: id('a')}, mock.NewHub(), o)
			defer a.Close()
			_, _ = a.JoinRoom(context.Background(), "")
			if _, err := a.Dial(context.Background(), contract.Peer{ID: id('z')}); err == nil {
				t.Fatal("dial to nobody succeeded")
			}
			mu.Lock()
			defer mu.Unlock()
			if len(got) != len(tc.want) {
				t.Fatalf("attempts %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("attempts %v, want %v", got, tc.want)
				}
			}
		})
	}
}

func TestPeerLeavingEndsSetupEarly(t *testing.T) {
	testutil.NoLeaks(t)
	hub := mock.NewHub()
	a := New(contract.Peer{ID: id('a')}, hub, opts()) // 5 s timeout
	defer a.Close()
	code, _ := a.JoinRoom(context.Background(), "")
	// A member that never answers, then leaves.
	silent, err := hub.Join(context.Background(), code, contract.Peer{ID: id('s')})
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for s := range silent.Signals() {
			if s.Kind == contract.SignalOffer {
				_ = silent.Close()
			}
		}
	}()
	start := time.Now()
	if _, err := a.Dial(context.Background(), contract.Peer{ID: id('s')}); err == nil {
		t.Fatal("dial succeeded")
	}
	if time.Since(start) > 3*time.Second {
		t.Fatal("a departed peer kept the dial waiting for the full timeout")
	}
}

func TestCloseUnblocksEverything(t *testing.T) {
	testutil.NoLeaks(t)
	a, b := pair(t)

	acc := make(chan error, 1)
	go func() { _, err := a.Accept(context.Background()); acc <- err }()

	sa, sb := connect(t, a, b)
	rd := make(chan error, 1)
	go func() { _, err := sb.Read(make([]byte, 10)); rd <- err }()

	// sb never reads more, so sa's buffer fills and Write must wait...
	wr := make(chan error, 1)
	go func() { _, err := sa.Write(make([]byte, 64<<20)); wr <- err }()
	time.Sleep(300 * time.Millisecond)
	select {
	case err := <-wr:
		t.Fatalf("64 MiB write to a peer that does not read returned early: %v", err)
	default:
	}
	// ...until the stream closes.
	_ = sa.Close()
	select {
	case err := <-wr:
		if err == nil {
			t.Fatal("blocked write reported success")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not end a blocked Write")
	}

	b.Close() // closes sb, which ends its Read
	select {
	case <-rd:
	case <-time.After(5 * time.Second):
		t.Fatal("transport Close did not end a blocked Read")
	}
	a.Close()
	if err := <-acc; !errors.Is(err, ErrClosed) {
		t.Fatalf("Accept after Close = %v", err)
	}
	if _, err := a.Dial(context.Background(), contract.Peer{ID: id('b')}); !errors.Is(err, ErrClosed) {
		t.Fatalf("Dial after Close = %v", err)
	}
	a.Close() // idempotent
}

func TestICEFromEnv(t *testing.T) {
	t.Setenv("DROPLINK_ICE", "")
	if s, err := ICEFromEnv(); err != nil || len(s) != 1 || hasTURN(s) {
		t.Fatalf("default: %v %v", s, err)
	}
	t.Setenv("DROPLINK_ICE", `[{"urls":["stun:s.example:3478"]},{"urls":["turn:t.example:3478"],"username":"u","credential":"p"}]`)
	s, err := ICEFromEnv()
	if err != nil || len(s) != 2 || !hasTURN(s) || s[1].Username != "u" {
		t.Fatalf("custom: %+v %v", s, err)
	}
	for _, bad := range []string{`not json`, `[{"urls":[]}]`, `[{"urls":["http://x"]}]`} {
		t.Setenv("DROPLINK_ICE", bad)
		if _, err := ICEFromEnv(); err == nil {
			t.Errorf("accepted %s", bad)
		}
	}
}

func TestFingerprints(t *testing.T) {
	sdp := "v=0\r\na=fingerprint:sha-256 AB:CD\r\nm=application\r\na=fingerprint:sha-256 AB:CD\r\n"
	if f := fingerprints(sdp); len(f) != 1 || f[0] != "sha-256 ab:cd" {
		t.Fatalf("got %v", f)
	}
}

package lan

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/vinayakkam/droplink/internal/contract"
	dc "github.com/vinayakkam/droplink/internal/crypto"
	"github.com/vinayakkam/droplink/internal/engine"
	"github.com/vinayakkam/droplink/internal/testutil"
)

type node struct {
	t    *Transport
	c    *dc.Crypto
	peer contract.Peer // how others dial it
}

func newNode(t *testing.T, opts Options) node {
	t.Helper()
	c, err := dc.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if opts.Addr == "" {
		opts.Addr = "127.0.0.1:0"
	}
	tr, err := New(contract.Peer{ID: c.ID(), Name: "n"}, c, opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { tr.Close() })
	return node{t: tr, c: c, peer: contract.Peer{ID: c.ID(), Addr: "127.0.0.1:" + strconv.Itoa(int(tr.Port()))}}
}

func ctx(t *testing.T) context.Context {
	c, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	return c
}

// connect dials b from a and returns both ends.
func connect(t *testing.T, a, b node) (contract.Stream, contract.Stream) {
	t.Helper()
	got := make(chan contract.Stream, 1)
	go func() {
		s, err := b.t.Accept(ctx(t))
		if err != nil {
			t.Error(err)
		}
		got <- s
	}()
	s, err := a.t.Dial(ctx(t), b.peer)
	if err != nil {
		t.Fatal(err)
	}
	r := <-got
	if r == nil {
		t.FailNow()
	}
	return s, r
}

type pattern struct{ size, pos int64 }

func (p *pattern) Read(b []byte) (int, error) {
	if p.pos >= p.size {
		return 0, io.EOF
	}
	n := int(min(int64(len(b)), p.size-p.pos))
	for i := range n {
		// Half text-like, half noisy: zstd gets something to do.
		v := p.pos + int64(i)
		if v/4096%2 == 0 {
			b[i] = "DROPLINK cargo manifest "[v%24]
		} else {
			b[i] = byte(uint64(v) * 0x9E3779B97F4A7C15 >> 56)
		}
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

func TestEngineOverRealLAN(t *testing.T) {
	testutil.NoLeaks(t)
	a, b := newNode(t, Options{}), newNode(t, Options{})
	sa, sb := connect(t, a, b)
	if sb.Remote().ID != a.c.ID() || sa.Remote().ID != b.c.ID() || sa.Remote().Route != contract.RouteLAN {
		t.Fatalf("remotes: %+v / %+v", sa.Remote(), sb.Remote())
	}
	if sa.(*stream).SAS() != sb.(*stream).SAS() {
		t.Fatal("SAS differs between the two ends")
	}
	e := engine.New(64 << 10)
	o := contract.Offer{ID: "lan", Files: []contract.FileMeta{{Path: "big.bin", Size: 64 << 20}}}
	sent := make(chan error, 1)
	go func() { sent <- e.Send(ctx(t), sa, o, src{}, nil) }()
	if _, err := e.Receive(ctx(t), sb, func(contract.Offer) bool { return true }, sink{}, nil); err != nil {
		t.Fatal(err)
	}
	if err := <-sent; err != nil {
		t.Fatal(err)
	}
}

func TestWrongPeerRefused(t *testing.T) {
	testutil.NoLeaks(t)
	a, b := newNode(t, Options{}), newNode(t, Options{})
	impostor := b.peer
	impostor.ID = "ffffffffffffffffffffffffffffffff"
	if _, err := a.t.Dial(ctx(t), impostor); !errors.Is(err, ErrWrongPeer) {
		t.Fatalf("err = %v", err)
	}
	if _, err := a.t.Dial(ctx(t), contract.Peer{ID: b.peer.ID}); err == nil {
		t.Fatal("dialed a peer with no address")
	}
}

func TestCompressionNegotiation(t *testing.T) {
	testutil.NoLeaks(t)
	for _, tc := range []struct {
		name       string
		aOff, bOff bool
		want       bool
	}{
		{"both on", false, false, true},
		{"listener off", false, true, false},
		{"dialer off", true, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, b := newNode(t, Options{NoCompress: tc.aOff}), newNode(t, Options{NoCompress: tc.bOff})
			sa, sb := connect(t, a, b)
			if sa.(*stream).compressed != tc.want || sb.(*stream).compressed != tc.want {
				t.Fatalf("compressed = %v/%v, want %v", sa.(*stream).compressed, sb.(*stream).compressed, tc.want)
			}
			data := bytes.Repeat([]byte("starfield "), 50_000)
			go func() { _, _ = sa.Write(data); sa.Close() }()
			got, err := io.ReadAll(sb)
			if err != nil || !bytes.Equal(got, data) {
				t.Fatalf("%d bytes, %v", len(got), err)
			}
			sb.Close()
		})
	}
}

func TestNegotiateRejectsUnofferedFeatures(t *testing.T) {
	x, y := net.Pipe()
	defer x.Close()
	defer y.Close()
	go func() {
		var b [1]byte
		_, _ = io.ReadFull(y, b[:])
		_, _ = y.Write([]byte{flagZstd}) // enables what was not offered
	}()
	if _, err := negotiate(x, true, 0); err == nil {
		t.Fatal("accepted a feature we did not offer")
	}
}

func TestLimiterMath(t *testing.T) {
	now := time.Unix(0, 0)
	l := newLimiter()
	l.now = func() time.Time { return now }
	if l.reserve(1<<30) != 0 {
		t.Fatal("unlimited limiter waited")
	}
	l.set(1 << 20) // 1 MiB/s
	var waited time.Duration
	for range 64 { // 64 × 32 KiB = 2 MiB
		d := l.reserve(32 << 10)
		waited += d
		now = now.Add(d)
	}
	if waited < 1800*time.Millisecond || waited > 2200*time.Millisecond {
		t.Fatalf("2 MiB at 1 MiB/s waited %v, want about 2 s", waited)
	}
	l.set(-5)
	if l.reserve(1) != 0 {
		t.Fatal("negative rate not treated as unlimited")
	}
}

func TestSpeedLimitRealTime(t *testing.T) {
	testutil.NoLeaks(t)
	a, b := newNode(t, Options{NoCompress: true}), newNode(t, Options{})
	a.t.SetLimit(4 << 20) // 4 MiB/s
	sa, sb := connect(t, a, b)
	go func() { _, _ = io.Copy(io.Discard, sb) }()
	payload := make([]byte, 2<<20)
	start := time.Now()
	if _, err := sa.Write(payload); err != nil {
		t.Fatal(err)
	}
	el := time.Since(start)
	// 2 MiB at 4 MiB/s ≈ 0.5 s, minus the 1 MiB burst allowance ≈ 0.25 s.
	if el < 200*time.Millisecond || el > 900*time.Millisecond {
		t.Fatalf("2 MiB took %v at 4 MiB/s", el)
	}
	sa.Close()
	sb.Close()
}

func TestCloseUnblocksEverything(t *testing.T) {
	testutil.NoLeaks(t)
	a, b := newNode(t, Options{}), newNode(t, Options{})

	acc := make(chan error, 1)
	go func() { _, err := b.t.Accept(context.Background()); acc <- err }()
	time.Sleep(20 * time.Millisecond)
	_ = b.t.Close()
	if err := <-acc; !errors.Is(err, ErrClosed) {
		t.Fatalf("Accept after Close = %v", err)
	}
	_ = b.t.Close() // idempotent
	if _, err := b.t.Dial(ctx(t), a.peer); !errors.Is(err, ErrClosed) {
		t.Fatalf("Dial after Close = %v", err)
	}

	c := newNode(t, Options{})
	sa, sc := connect(t, a, c)
	rd := make(chan error, 1)
	go func() { _, err := sa.Read(make([]byte, 1)); rd <- err }()
	time.Sleep(20 * time.Millisecond)
	_ = a.t.Close() // closing the transport closes its streams
	if err := <-rd; err == nil {
		t.Fatal("Read survived transport Close")
	}
	sc.Close()

	// A write stuck in the speed limiter ends when the stream closes.
	d, e := newNode(t, Options{}), newNode(t, Options{})
	d.t.SetLimit(1024)
	sd, se := connect(t, d, e)
	go func() { _, _ = io.Copy(io.Discard, se) }()
	wr := make(chan error, 1)
	go func() { _, err := sd.Write(make([]byte, 1<<20)); wr <- err }()
	time.Sleep(50 * time.Millisecond)
	sd.Close()
	select {
	case err := <-wr:
		if err == nil {
			t.Fatal("limited write finished instantly")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("limited write ignored Close")
	}
	se.Close()
}

func TestSilentClientIsDropped(t *testing.T) {
	testutil.NoLeaks(t)
	b := newNode(t, Options{HandshakeTimeout: 100 * time.Millisecond})
	raw, err := net.Dial("tcp", b.peer.Addr)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	_ = raw.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := raw.Read(make([]byte, 1)); err == nil {
		t.Fatal("silent client kept open")
	}
	// The transport still works for real peers.
	a := newNode(t, Options{})
	sa, sb := connect(t, a, b)
	sa.Close()
	sb.Close()
}

func TestFullQueueRefusesNewStreams(t *testing.T) {
	testutil.NoLeaks(t)
	a, b := newNode(t, Options{}), newNode(t, Options{MaxPending: 1})
	var wg sync.WaitGroup
	streams := make([]contract.Stream, 2)
	for i := range streams {
		s, err := a.t.Dial(ctx(t), b.peer)
		if err != nil {
			t.Fatal(err)
		}
		streams[i] = s
	}
	// Nobody called Accept: the second stream is closed by b.
	_ = streams[1].(*stream).conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := streams[1].Read(make([]byte, 1)); err == nil {
		t.Fatal("second stream accepted beyond MaxPending")
	}
	if s, err := b.t.Accept(ctx(t)); err != nil {
		t.Fatal(err)
	} else {
		s.Close()
	}
	for _, s := range streams {
		s.Close()
	}
	wg.Wait()
}

func TestAddrFromEnv(t *testing.T) {
	t.Setenv("DROPLINK_LAN_PORT", "")
	if AddrFromEnv() != ":0" {
		t.Fatal(AddrFromEnv())
	}
	t.Setenv("DROPLINK_LAN_PORT", "47500")
	if AddrFromEnv() != ":47500" {
		t.Fatal(AddrFromEnv())
	}
}

func TestOnPortReported(t *testing.T) {
	var got uint16
	n := newNode(t, Options{OnPort: func(p uint16) { got = p }})
	if got == 0 || got != n.t.Port() {
		t.Fatalf("OnPort %d, Port %d", got, n.t.Port())
	}
}

func TestNeedsCrypto(t *testing.T) {
	if _, err := New(contract.Peer{}, nil, Options{Addr: "127.0.0.1:0"}); err == nil {
		t.Fatal("transport without crypto")
	}
}

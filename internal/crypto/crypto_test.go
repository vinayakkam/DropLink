package crypto

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/vinayakkam/droplink/internal/contract"
	"github.com/vinayakkam/droplink/internal/testutil"
)

func seed(b byte) []byte { return bytes.Repeat([]byte{b}, 32) }

func newCrypto(t *testing.T, b byte) *Crypto {
	t.Helper()
	return &Crypto{id: NewIdentity(seed(b)), trust: &trustStore{path: filepath.Join(t.TempDir(), TrustFile), devs: map[contract.DeviceID]contract.TrustedDevice{}}}
}

// pair runs Secure on both ends of a net.Pipe.
func pair(t *testing.T, a, b *Crypto) (contract.SecureConn, contract.SecureConn) {
	t.Helper()
	x, y := net.Pipe()
	t.Cleanup(func() { x.Close(); y.Close() })
	type res struct {
		c   contract.SecureConn
		err error
	}
	ch := make(chan res, 1)
	go func() { c, err := b.Secure(context.Background(), y, false); ch <- res{c, err} }()
	ca, err := a.Secure(context.Background(), x, true)
	if err != nil {
		t.Fatal(err)
	}
	r := <-ch
	if r.err != nil {
		t.Fatal(r.err)
	}
	return ca, r.c
}

func TestIdentityPersists(t *testing.T) {
	dir := t.TempDir()
	a, err := LoadIdentity(dir)
	if err != nil {
		t.Fatal(err)
	}
	b, err := LoadIdentity(dir)
	if err != nil {
		t.Fatal(err)
	}
	if a.ID() != b.ID() || len(a.ID()) != 32 {
		t.Fatalf("ids %q %q", a.ID(), b.ID())
	}
	if runtime.GOOS != "windows" {
		info, _ := os.Stat(filepath.Join(dir, KeyFile))
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("key mode %v", info.Mode().Perm())
		}
	}
	other, _ := LoadIdentity(t.TempDir())
	if other.ID() == a.ID() {
		t.Fatal("two fresh keys share an ID")
	}
}

func TestDamagedKeyIsNotReplaced(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, KeyFile)
	_ = os.WriteFile(path, []byte("short"), 0o600)
	if _, err := LoadIdentity(dir); err == nil {
		t.Fatal("damaged key accepted")
	}
	if b, _ := os.ReadFile(path); string(b) != "short" {
		t.Fatal("damaged key was overwritten")
	}
}

func TestHandshakeAgrees(t *testing.T) {
	testutil.NoLeaks(t)
	a, b := newCrypto(t, 1), newCrypto(t, 2)
	ca, cb := pair(t, a, b)
	if ca.RemoteID() != b.ID() || cb.RemoteID() != a.ID() {
		t.Fatal("remote IDs wrong")
	}
	if ca.SAS() != cb.SAS() {
		t.Fatalf("SAS %v vs %v", ca.SAS(), cb.SAS())
	}
	for _, g := range ca.SAS() {
		if g > 63 {
			t.Fatal("glyph out of range")
		}
	}
	// Fresh ephemeral keys: a second session has a different SAS (with
	// overwhelming probability), so an old SAS cannot be replayed.
	ca2, _ := pair(t, a, b)
	if ca2.SAS() == ca.SAS() {
		t.Fatal("SAS repeated across sessions")
	}
}

func TestDataRoundTripBothWays(t *testing.T) {
	testutil.NoLeaks(t)
	ca, cb := pair(t, newCrypto(t, 1), newCrypto(t, 2))
	big := make([]byte, 1<<20+123) // spans many records, odd tail
	for i := range big {
		big[i] = byte(i * 7)
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); _, _ = ca.Write(big) }()
	go func() { defer wg.Done(); _, _ = cb.Write([]byte("ack")) }()
	got := make([]byte, len(big))
	if _, err := io.ReadFull(cb, got); err != nil || !bytes.Equal(got, big) {
		t.Fatalf("a→b: %v", err)
	}
	ack := make([]byte, 3)
	if _, err := io.ReadFull(ca, ack); err != nil || string(ack) != "ack" {
		t.Fatalf("b→a: %q %v", ack, err)
	}
	wg.Wait()
}

// tap records what crosses the wire and can rewrite it.
type tap struct {
	net.Conn
	mu      sync.Mutex
	written [][]byte
	mutate  func(n int, p []byte) []byte // n = index of this Write
	n       int
}

func (t *tap) Write(p []byte) (int, error) {
	t.mu.Lock()
	q := bytes.Clone(p)
	t.written = append(t.written, q)
	if t.mutate != nil {
		q = t.mutate(t.n, q)
	}
	t.n++
	t.mu.Unlock()
	if _, err := t.Conn.Write(q); err != nil {
		return 0, err
	}
	return len(p), nil
}

// securedTap gives a sender whose raw writes after the handshake go
// through the tap, and the receiving SecureConn.
func securedTap(t *testing.T, mutate func(int, []byte) []byte) (contract.SecureConn, contract.SecureConn, *tap) {
	t.Helper()
	a, b := newCrypto(t, 1), newCrypto(t, 2)
	x, y := net.Pipe()
	t.Cleanup(func() { x.Close(); y.Close() })
	tp := &tap{Conn: x}
	ch := make(chan contract.SecureConn, 1)
	go func() { c, _ := b.Secure(context.Background(), y, false); ch <- c }()
	ca, err := a.Secure(context.Background(), tp, true)
	if err != nil {
		t.Fatal(err)
	}
	cb := <-ch
	tp.mu.Lock()
	start := tp.n // handshake writes so far
	tp.mutate = func(n int, p []byte) []byte {
		if mutate == nil {
			return p
		}
		return mutate(n-start, p)
	}
	tp.mu.Unlock()
	return ca, cb, tp
}

func TestPlaintextNeverOnTheWire(t *testing.T) {
	ca, cb, tp := securedTap(t, nil)
	secret := []byte("coordinates of the hidden base")
	go func() { _, _ = ca.Write(secret) }()
	buf := make([]byte, len(secret))
	if _, err := io.ReadFull(cb, buf); err != nil {
		t.Fatal(err)
	}
	tp.mu.Lock()
	defer tp.mu.Unlock()
	for _, w := range tp.written {
		if bytes.Contains(w, secret) {
			t.Fatal("plaintext seen on the wire")
		}
	}
}

func TestFlippedByteIsRejected(t *testing.T) {
	ca, cb, _ := securedTap(t, func(n int, p []byte) []byte {
		if n == 0 {
			p[len(p)-1] ^= 1 // inside the tag
		}
		return p
	})
	go func() { _, _ = ca.Write([]byte("hello")) }()
	if _, err := cb.Read(make([]byte, 8)); !errors.Is(err, ErrRecord) {
		t.Fatalf("err = %v, want ErrRecord", err)
	}
	if _, err := cb.Read(make([]byte, 8)); !errors.Is(err, ErrRecord) {
		t.Fatal("stream kept working after a forged record")
	}
}

func TestReplayedRecordIsRejected(t *testing.T) {
	var first []byte
	ca, cb, _ := securedTap(t, func(n int, p []byte) []byte {
		switch n {
		case 0:
			first = bytes.Clone(p)
		case 1:
			return first // replay record 0 in place of record 1
		}
		return p
	})
	go func() { _, _ = ca.Write([]byte("one")); _, _ = ca.Write([]byte("two")) }()
	buf := make([]byte, 3)
	if _, err := io.ReadFull(cb, buf); err != nil || string(buf) != "one" {
		t.Fatalf("first record: %q %v", buf, err)
	}
	if _, err := cb.Read(buf); !errors.Is(err, ErrRecord) {
		t.Fatalf("replay accepted: %v", err)
	}
}

func TestOversizedRecordHeaderRejected(t *testing.T) {
	_, cb, tp := securedTap(t, nil)
	go func() {
		var h [2]byte
		binary.BigEndian.PutUint16(h[:], 0xffff)
		_, _ = tp.Conn.Write(h[:])
	}()
	if _, err := cb.Read(make([]byte, 1)); !errors.Is(err, ErrRecord) {
		t.Fatalf("err = %v", err)
	}
}

func TestImpostorFailsHandshake(t *testing.T) {
	testutil.NoLeaks(t)
	// A man in the middle relays the hello but signs with its own key while
	// claiming the real responder's identity: the initiator must refuse.
	real, mitm := NewIdentity(seed(2)), NewIdentity(seed(9))
	x, y := net.Pipe()
	defer x.Close()
	defer y.Close()
	go func() {
		var m1 [msg1Len]byte
		if _, err := io.ReadFull(y, m1[:]); err != nil {
			return
		}
		var m2 [msg2Len]byte
		copy(m2[:32], make([]byte, 32))
		copy(m2[32:64], real.pub)                                                                        // claims to be real
		copy(m2[64:], ed25519.Sign(mitm.priv, signed("resp", transcriptHash(m1[:], m2[:32], real.pub)))) // right transcript, wrong key
		_, _ = y.Write(m2[:])
	}()
	_, err := Handshake(x, NewIdentity(seed(1)), true)
	if !errors.Is(err, ErrHandshake) {
		t.Fatalf("err = %v, want ErrHandshake", err)
	}
}

func TestNotDropLinkAndSelfRejected(t *testing.T) {
	x, y := net.Pipe()
	defer x.Close()
	defer y.Close()
	go func() { _, _ = x.Write(bytes.Repeat([]byte("G"), msg1Len)) }()
	if _, err := Handshake(y, NewIdentity(seed(1)), false); !errors.Is(err, ErrHandshake) {
		t.Fatalf("garbage hello: %v", err)
	}

	me := newCrypto(t, 5)
	me.HandshakeTimeout = 300 * time.Millisecond // the initiator waits for a reply that never comes
	p, q := net.Pipe()
	defer p.Close()
	defer q.Close()
	errs := make(chan error, 1)
	go func() { _, err := me.Secure(context.Background(), q, false); errs <- err }()
	if _, err := me.Secure(context.Background(), p, true); !errors.Is(err, ErrHandshake) {
		t.Fatalf("self connection: %v", err)
	}
	p.Close()
	<-errs
}

func TestSilentPeerTimesOut(t *testing.T) {
	testutil.NoLeaks(t)
	c := newCrypto(t, 1)
	c.HandshakeTimeout = 100 * time.Millisecond
	x, y := net.Pipe()
	defer x.Close()
	defer y.Close()
	start := time.Now()
	if _, err := c.Secure(context.Background(), x, true); err == nil {
		t.Fatal("handshake with a silent peer succeeded")
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("timeout not applied")
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(50 * time.Millisecond); cancel() }()
	p, q := net.Pipe()
	defer p.Close()
	defer q.Close()
	c.HandshakeTimeout = time.Minute
	if _, err := c.Secure(ctx, p, false); err == nil {
		t.Fatal("canceled handshake succeeded")
	}
}

func TestTrustPersists(t *testing.T) {
	dir := t.TempDir()
	c, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	id := IDFor(NewIdentity(seed(7)).pub)
	if err := c.Trust(contract.TrustedDevice{ID: "nope", Name: "x"}); err == nil {
		t.Fatal("invalid ID trusted")
	}
	if err := c.Trust(contract.TrustedDevice{ID: id, Name: "Scout"}); err != nil {
		t.Fatal(err)
	}
	first := c.Trusted()[0].Added
	_ = c.Trust(contract.TrustedDevice{ID: id, Name: "Scout II"})

	again, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !again.IsTrusted(id) || again.Trusted()[0].Name != "Scout II" || !again.Trusted()[0].Added.Equal(first) {
		t.Fatalf("after reload: %+v", again.Trusted())
	}
	if again.ID() != c.ID() {
		t.Fatal("identity changed across reload")
	}
	_ = again.Untrust(id)
	_ = again.Untrust(id) // unknown is fine
	third, _ := Load(dir)
	if third.IsTrusted(id) {
		t.Fatal("untrust not saved")
	}
}

func TestDamagedTrustFileReported(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, TrustFile), []byte("{not json"), 0o600)
	if _, err := Load(dir); err == nil {
		t.Fatal("damaged trust file accepted")
	}
}

// discardConn swallows writes, so the benchmark measures only the
// record layer, not net.Pipe.
type discardConn struct{ net.Conn }

func (discardConn) Write(p []byte) (int, error) { return len(p), nil }

func BenchmarkSecureConn(b *testing.B) {
	sess := &Session{Remote: NewIdentity(seed(2)).pub}
	c, err := newConn(discardConn{}, sess)
	if err != nil {
		b.Fatal(err)
	}
	buf := make([]byte, 64<<10)
	b.SetBytes(int64(len(buf)))
	b.ReportAllocs()
	for b.Loop() {
		if _, err := c.Write(buf); err != nil {
			b.Fatal(err)
		}
	}
}

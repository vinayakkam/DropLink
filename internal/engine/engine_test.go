package engine_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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

const chunk = 64 << 10

var (
	alice = contract.Peer{ID: "alice", Name: "Alice"}
	bob   = contract.Peer{ID: "bob", Name: "Bob"}
)

// patternFile is a deterministic, seekable file of any size that uses no
// memory, for big transfers.
type patternFile struct{ size, pos int64 }

func (p *patternFile) Read(b []byte) (int, error) {
	if p.pos >= p.size {
		return 0, io.EOF
	}
	n := int(min(int64(len(b)), p.size-p.pos))
	for i := range n {
		x := uint64(p.pos+int64(i)) * 0x9E3779B97F4A7C15
		b[i] = byte(x >> 56)
	}
	p.pos += int64(n)
	return n, nil
}

func (p *patternFile) Seek(off int64, whence int) (int64, error) {
	switch whence {
	case io.SeekStart:
		p.pos = off
	case io.SeekCurrent:
		p.pos += off
	case io.SeekEnd:
		p.pos = p.size + off
	}
	return p.pos, nil
}

func (p *patternFile) Close() error { return nil }

type patternSrc struct{}

func (patternSrc) Open(f contract.FileMeta) (io.ReadSeekCloser, error) {
	return &patternFile{size: f.Size}, nil
}

// discardSink verifies by hashing and keeps nothing.
type discardSink struct {
	mu        sync.Mutex
	committed []string
	aborted   []string
}

type nopWC struct{ io.Writer }

func (nopWC) Close() error { return nil }

func (d *discardSink) Open(contract.FileMeta) (io.WriteCloser, int64, error) {
	return nopWC{io.Discard}, 0, nil
}
func (d *discardSink) Commit(f contract.FileMeta) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.committed = append(d.committed, f.Path)
	return nil
}
func (d *discardSink) Abort(f contract.FileMeta, _ error) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.aborted = append(d.aborted, f.Path)
	return nil
}

// countingStream counts bytes written and can corrupt one byte.
type countingStream struct {
	contract.Stream
	written   atomic.Int64
	corruptAt int64 // 0 = never; else flip the byte at this write offset
}

func (c *countingStream) Write(p []byte) (int, error) {
	start := c.written.Load()
	if c.corruptAt > 0 && start <= c.corruptAt && c.corruptAt < start+int64(len(p)) {
		q := bytes.Clone(p)
		q[c.corruptAt-start] ^= 0xff
		p = q
	}
	n, err := c.Stream.Write(p)
	c.written.Add(int64(n))
	return n, err
}

type result struct {
	offer contract.Offer
	err   error
}

// transfer runs Send and Receive concurrently and returns both errors.
func transfer(t *testing.T, sctx, rctx context.Context, o contract.Offer, src contract.Source, dst contract.Sink,
	wrap func(contract.Stream) contract.Stream, accept bool, sp, rp func(contract.Progress)) (result, error) {
	t.Helper()
	e := engine.New(chunk)
	a, b := mock.Pipe(alice, bob)
	if wrap != nil {
		a = wrap(a)
	}
	sent := make(chan error, 1)
	go func() { sent <- e.Send(sctx, a, o, src, sp) }()
	got, err := e.Receive(rctx, b, func(contract.Offer) bool { return accept }, dst, rp)
	select {
	case serr := <-sent:
		return result{got, err}, serr
	case <-time.After(10 * time.Second):
		t.Fatal("Send did not return")
	}
	return result{}, nil
}

func bg() context.Context { return context.Background() }

func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, body := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestFolderRoundTripOnDisk(t *testing.T) {
	testutil.NoLeaks(t)
	in, out := t.TempDir(), t.TempDir()
	big := strings.Repeat("pixel-cargo-", 40_000) // ~480 KB: several chunks
	writeTree(t, in, map[string]string{
		"Mission/log.txt":         "launch at 0300",
		"Mission/maps/sector.bin": big,
		"Mission/empty.dat":       "",
	})
	metas, src, err := files.Walk([]string{filepath.Join(in, "Mission")})
	if err != nil {
		t.Fatal(err)
	}
	sink, err := files.NewSink(out)
	if err != nil {
		t.Fatal(err)
	}
	var last contract.Progress
	r, serr := transfer(t, bg(), bg(), contract.Offer{ID: "t1", From: alice, Files: metas}, src, sink, nil, true,
		nil, func(p contract.Progress) { last = p })
	if serr != nil || r.err != nil {
		t.Fatalf("send=%v recv=%v", serr, r.err)
	}
	if last.State != contract.StateDone || last.Done != last.Total || last.Total != int64(len(big)+14) {
		t.Fatalf("last progress = %+v", last)
	}
	for rel, want := range map[string]string{"Mission/log.txt": "launch at 0300", "Mission/maps/sector.bin": big, "Mission/empty.dat": ""} {
		got, err := os.ReadFile(filepath.Join(out, filepath.FromSlash(rel)))
		if err != nil || string(got) != want {
			t.Fatalf("%s: %d bytes, %v", rel, len(got), err)
		}
	}
	parts, _ := filepath.Glob(filepath.Join(out, "Mission", "*"+files.PartSuffix))
	if len(parts) != 0 {
		t.Fatalf("part files left: %v", parts)
	}
}

func TestTextShare(t *testing.T) {
	testutil.NoLeaks(t)
	o := contract.Offer{ID: "clip", From: alice, Text: "https://example.test/coords"}
	r, serr := transfer(t, bg(), bg(), o, files.Text{}, &discardSink{}, nil, true, nil, nil)
	if serr != nil || r.err != nil || r.offer.Text != o.Text {
		t.Fatalf("send=%v recv=%v text=%q", serr, r.err, r.offer.Text)
	}
}

func TestReject(t *testing.T) {
	testutil.NoLeaks(t)
	var states []contract.TransferState
	o := contract.Offer{ID: "r", Files: []contract.FileMeta{{Path: "a", Size: 10}}}
	r, serr := transfer(t, bg(), bg(), o, patternSrc{}, &discardSink{}, nil, false,
		func(p contract.Progress) { states = append(states, p.State) }, nil)
	if !errors.Is(serr, engine.ErrRejected) || !errors.Is(r.err, engine.ErrRejected) {
		t.Fatalf("send=%v recv=%v", serr, r.err)
	}
	if states[len(states)-1] != contract.StateRejected {
		t.Fatalf("states = %v", states)
	}
}

// The sender goes away while the receiver is still deciding: Receive
// ends at once instead of waiting for an answer nobody needs.
func TestSenderLeavesWhileDeciding(t *testing.T) {
	for _, how := range []string{"cancel", "hang up"} {
		t.Run(how, func(t *testing.T) {
			testutil.NoLeaks(t)
			e := engine.New(chunk)
			a, b := mock.Pipe(alice, bob)
			sctx, cancel := context.WithCancel(bg())
			defer cancel()
			sent := make(chan error, 1)
			o := contract.Offer{ID: "gone", Files: []contract.FileMeta{{Path: "a", Size: 10}}}
			go func() { sent <- e.Send(sctx, a, o, patternSrc{}, nil) }()
			release := make(chan struct{})
			defer close(release) // the user answers only after Receive gave up
			start := time.Now()
			_, err := e.Receive(bg(), b, func(contract.Offer) bool {
				if how == "cancel" {
					cancel()
				} else {
					_ = a.Close()
				}
				<-release
				return true
			}, &discardSink{}, nil)
			if !errors.Is(err, engine.ErrCanceled) {
				t.Fatalf("recv = %v", err)
			}
			if d := time.Since(start); d > 5*time.Second {
				t.Fatalf("Receive took %v", d)
			}
			<-sent
		})
	}
}

// Accepting after the watcher began reading loses no byte of the stream.
func TestAcceptAfterDelayKeepsStream(t *testing.T) {
	testutil.NoLeaks(t)
	e := engine.New(chunk)
	a, b := mock.Pipe(alice, bob)
	sent := make(chan error, 1)
	o := contract.Offer{ID: "slow", Files: []contract.FileMeta{{Path: "a", Size: 3*chunk + 7}}}
	go func() { sent <- e.Send(bg(), a, o, patternSrc{}, nil) }()
	dst := &discardSink{}
	_, err := e.Receive(bg(), b, func(contract.Offer) bool {
		time.Sleep(50 * time.Millisecond)
		return true
	}, dst, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := <-sent; err != nil {
		t.Fatal(err)
	}
}

func TestResumeSkipsStoredBytes(t *testing.T) {
	testutil.NoLeaks(t)
	out := t.TempDir()
	const size = 1 << 20
	full, _ := io.ReadAll(&patternFile{size: size})
	// A previous attempt stored the first 600 KB.
	if err := os.WriteFile(filepath.Join(out, "big.bin"+files.PartSuffix), full[:600<<10], 0o644); err != nil {
		t.Fatal(err)
	}
	sink, _ := files.NewSink(out)
	var cs *countingStream
	o := contract.Offer{ID: "res", Files: []contract.FileMeta{{Path: "big.bin", Size: size}}}
	var first contract.Progress
	r, serr := transfer(t, bg(), bg(), o, patternSrc{}, sink,
		func(s contract.Stream) contract.Stream { cs = &countingStream{Stream: s}; return cs }, true,
		func(p contract.Progress) {
			if first.State == "" && p.State == contract.StateActive {
				first = p
			}
		}, nil)
	if serr != nil || r.err != nil {
		t.Fatalf("send=%v recv=%v", serr, r.err)
	}
	got, _ := os.ReadFile(filepath.Join(out, "big.bin"))
	if !bytes.Equal(got, full) {
		t.Fatal("resumed file differs from the original")
	}
	if w := cs.written.Load(); w > size-(600<<10)+64<<10 {
		t.Fatalf("sender wrote %d bytes; it should have skipped the stored 600 KB", w)
	}
	if first.Done != 600<<10 {
		t.Fatalf("first active progress Done = %d, want the resumed 600 KB", first.Done)
	}
}

func TestCorruptPartFileIsDetectedAndDropped(t *testing.T) {
	testutil.NoLeaks(t)
	out := t.TempDir()
	part := filepath.Join(out, "x.bin"+files.PartSuffix)
	_ = os.WriteFile(part, bytes.Repeat([]byte{0xAA}, 1000), 0o644) // wrong bytes
	sink, _ := files.NewSink(out)
	o := contract.Offer{ID: "c", Files: []contract.FileMeta{{Path: "x.bin", Size: 5000}}}
	r, _ := transfer(t, bg(), bg(), o, patternSrc{}, sink, nil, true, nil, nil)
	if !errors.Is(r.err, engine.ErrCorrupt) {
		t.Fatalf("recv = %v, want ErrCorrupt", r.err)
	}
	if _, err := os.Stat(part); !os.IsNotExist(err) {
		t.Fatal("corrupt part file kept; the retry would resume garbage")
	}
	if _, err := os.Stat(filepath.Join(out, "x.bin")); !os.IsNotExist(err) {
		t.Fatal("corrupt file was committed")
	}
}

func TestCorruptionInFlight(t *testing.T) {
	testutil.NoLeaks(t)
	sink := &discardSink{}
	o := contract.Offer{ID: "f", Files: []contract.FileMeta{{Path: "a", Size: 300_000}}}
	r, serr := transfer(t, bg(), bg(), o, patternSrc{}, sink,
		func(s contract.Stream) contract.Stream { return &countingStream{Stream: s, corruptAt: 200_000} }, true, nil, nil)
	if !errors.Is(r.err, engine.ErrCorrupt) {
		t.Fatalf("recv = %v", r.err)
	}
	if serr == nil || !errors.Is(serr, engine.ErrRemote) {
		t.Fatalf("send = %v, want the receiver's error", serr)
	}
	if len(sink.committed) != 0 || len(sink.aborted) != 1 {
		t.Fatalf("committed=%v aborted=%v", sink.committed, sink.aborted)
	}
}

func cancelAfterFirstChunk(cancel context.CancelFunc) func(contract.Progress) {
	var once sync.Once
	return func(p contract.Progress) {
		if p.Done > 0 {
			once.Do(cancel)
		}
	}
}

func TestCancelBySender(t *testing.T) {
	testutil.NoLeaks(t)
	ctx, cancel := context.WithCancel(bg())
	defer cancel()
	o := contract.Offer{ID: "cs", Files: []contract.FileMeta{{Path: "a", Size: 1 << 30}}}
	r, serr := transfer(t, ctx, bg(), o, patternSrc{}, &discardSink{}, nil, true, cancelAfterFirstChunk(cancel), nil)
	if !errors.Is(serr, engine.ErrCanceled) || !errors.Is(r.err, engine.ErrCanceled) {
		t.Fatalf("send=%v recv=%v", serr, r.err)
	}
}

func TestCancelByReceiver(t *testing.T) {
	testutil.NoLeaks(t)
	ctx, cancel := context.WithCancel(bg())
	defer cancel()
	var last contract.Progress
	o := contract.Offer{ID: "cr", Files: []contract.FileMeta{{Path: "a", Size: 1 << 30}}}
	r, serr := transfer(t, bg(), ctx, o, patternSrc{}, &discardSink{}, nil, true, nil,
		func(p contract.Progress) { last = p; cancelAfterFirstChunk(cancel)(p) })
	if !errors.Is(serr, engine.ErrCanceled) || !errors.Is(r.err, engine.ErrCanceled) {
		t.Fatalf("send=%v recv=%v", serr, r.err)
	}
	if last.State != contract.StateCancelled {
		t.Fatalf("last state = %q", last.State)
	}
}

func TestPauseAndResumeFromReceiver(t *testing.T) {
	testutil.NoLeaks(t)
	ctl := engine.NewControl()
	ctl.Pause() // the receiver starts paused: no data may flow until Resume
	var mu sync.Mutex
	var states []contract.TransferState
	var senderDone atomic.Int64
	paused := make(chan struct{})
	var once sync.Once
	checked := make(chan struct{})

	go func() {
		defer close(checked)
		select {
		case <-paused:
		case <-time.After(5 * time.Second):
			t.Error("sender never reported paused")
			ctl.Resume()
			return
		}
		at := senderDone.Load()
		time.Sleep(150 * time.Millisecond)
		if moved := senderDone.Load() - at; moved > 2*chunk {
			t.Errorf("sender moved %d bytes while paused", moved)
		}
		ctl.Resume()
	}()
	o := contract.Offer{ID: "p", Files: []contract.FileMeta{{Path: "a", Size: 8 << 20}}}
	r, serr := transfer(t, bg(), engine.WithControl(bg(), ctl), o, patternSrc{}, &discardSink{}, nil, true,
		func(p contract.Progress) {
			senderDone.Store(p.Done)
			mu.Lock()
			states = append(states, p.State)
			mu.Unlock()
			if p.State == contract.StatePaused {
				once.Do(func() { close(paused) })
			}
		}, nil)
	<-checked
	if serr != nil || r.err != nil {
		t.Fatalf("send=%v recv=%v", serr, r.err)
	}
	mu.Lock()
	defer mu.Unlock()
	sawPause, sawActiveAfter := false, false
	for _, s := range states {
		if s == contract.StatePaused {
			sawPause = true
		} else if sawPause && s == contract.StateActive {
			sawActiveAfter = true
		}
	}
	if !sawPause || !sawActiveAfter || states[len(states)-1] != contract.StateDone {
		t.Fatalf("sender states = %v", states)
	}
}

func TestReceiverRejectsHostileOffers(t *testing.T) {
	testutil.NoLeaks(t)
	for _, p := range []string{"../evil", "/etc/passwd", "a/../../b", `C:\x`, "a\\b", "", "a//b", "./a"} {
		o := contract.Offer{ID: "h", Files: []contract.FileMeta{{Path: "ok", Size: 1}, {Path: p, Size: 1}}}
		// Validation runs on the sender too, so bypass it: write the frame raw.
		e := engine.New(chunk)
		a, b := mock.Pipe(alice, bob)
		go func() {
			js := []byte(`{"id":"h","files":[{"path":"ok","size":1},{"path":` + quote(p) + `,"size":1}]}`)
			hdr := []byte{1, 0, 0, 0, byte(len(js))}
			_, _ = a.Write(append(hdr, js...))
			_, _ = io.Copy(io.Discard, a)
		}()
		_, err := e.Receive(bg(), b, func(contract.Offer) bool { t.Fatal("asked to accept a hostile offer"); return true }, &discardSink{}, nil)
		if !errors.Is(err, engine.ErrProtocol) {
			t.Errorf("path %q: err = %v", p, err)
		}
		a.Close()
		_ = o
	}
}

func quote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		if r == '\\' || r == '"' {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	b.WriteByte('"')
	return b.String()
}

func TestSenderRejectsBadOffers(t *testing.T) {
	e := engine.New(chunk)
	for name, o := range map[string]contract.Offer{
		"no id":     {Files: []contract.FileMeta{{Path: "a"}}},
		"duplicate": {ID: "d", Files: []contract.FileMeta{{Path: "a"}, {Path: "A"}}},
		"negative":  {ID: "n", Files: []contract.FileMeta{{Path: "a", Size: -1}}},
	} {
		a, b := mock.Pipe(alice, bob)
		err := e.Send(bg(), a, o, patternSrc{}, nil)
		b.Close()
		if !errors.Is(err, engine.ErrProtocol) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestOversizedControlFrameRejected(t *testing.T) {
	testutil.NoLeaks(t)
	e := engine.New(chunk)
	a, b := mock.Pipe(alice, bob)
	go func() { _, _ = a.Write([]byte{1, 0x7f, 0xff, 0xff, 0xff}); _, _ = io.Copy(io.Discard, a) }()
	_, err := e.Receive(bg(), b, func(contract.Offer) bool { return true }, &discardSink{}, nil)
	a.Close()
	if !errors.Is(err, engine.ErrProtocol) {
		t.Fatalf("err = %v", err)
	}
}

func TestCleanPath(t *testing.T) {
	for in, want := range map[string]string{"a": "a", "dir/file.txt": "dir/file.txt", "a b/ç.png": "a b/ç.png"} {
		if got, err := engine.CleanPath(in); err != nil || got != want {
			t.Errorf("CleanPath(%q) = %q, %v", in, got, err)
		}
	}
}

func FuzzCleanPath(f *testing.F) {
	for _, s := range []string{"a/b", "../x", "/abs", "a/./b", `c:\w`, "ok.txt"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, p string) {
		got, err := engine.CleanPath(p)
		if err != nil {
			return
		}
		if strings.HasPrefix(got, "/") || strings.Contains(got, "..") && containsDotDotSeg(got) ||
			strings.ContainsAny(got, "\\:\x00") || filepath.IsAbs(got) {
			t.Fatalf("CleanPath(%q) = %q accepted an unsafe path", p, got)
		}
	})
}

func containsDotDotSeg(p string) bool {
	for _, s := range strings.Split(p, "/") {
		if s == ".." {
			return true
		}
	}
	return false
}

func TestProgressIsRateLimited(t *testing.T) {
	var n atomic.Int32
	o := contract.Offer{ID: "rl", Files: []contract.FileMeta{{Path: "a", Size: 32 << 20}}} // 512 chunks
	r, serr := transfer(t, bg(), bg(), o, patternSrc{}, &discardSink{}, nil, true,
		func(contract.Progress) { n.Add(1) }, nil)
	if serr != nil || r.err != nil {
		t.Fatalf("send=%v recv=%v", serr, r.err)
	}
	if got := n.Load(); got > 100 {
		t.Fatalf("%d progress callbacks for 512 chunks; want rate-limited", got)
	}
}

func TestMemoryStaysFlat(t *testing.T) {
	if testing.Short() {
		t.Skip("moves 256 MiB")
	}
	testutil.NoLeaks(t)
	runtime.GC()
	var peak atomic.Uint64
	stop := make(chan struct{})
	sampled := make(chan struct{})
	go func() {
		defer close(sampled)
		var ms runtime.MemStats
		for {
			select {
			case <-stop:
				return
			case <-time.After(5 * time.Millisecond):
				runtime.ReadMemStats(&ms)
				if ms.HeapInuse > peak.Load() {
					peak.Store(ms.HeapInuse)
				}
			}
		}
	}()
	o := contract.Offer{ID: "mem", Files: []contract.FileMeta{{Path: "a", Size: 256 << 20}}}
	r, serr := transfer(t, bg(), bg(), o, patternSrc{}, &discardSink{}, nil, true, nil, nil)
	close(stop)
	<-sampled
	if serr != nil || r.err != nil {
		t.Fatalf("send=%v recv=%v", serr, r.err)
	}
	if p := peak.Load(); p > 8<<20 {
		t.Fatalf("heap peaked at %d MiB moving 256 MiB; want under 8", p>>20)
	}
}

func TestHashCoversWholeFile(t *testing.T) {
	// Sanity check on the test helper: patternFile is not all one byte, so
	// a skipped prefix that was hashed wrongly would be caught.
	a, _ := io.ReadAll(&patternFile{size: 4096})
	if sha256.Sum256(a[:2048]) == sha256.Sum256(a[2048:]) {
		t.Fatal("pattern repeats; resume tests would be weak")
	}
}

func BenchmarkSend(b *testing.B) {
	e := engine.New(chunk)
	const size = 64 << 20
	o := contract.Offer{ID: "bench", Files: []contract.FileMeta{{Path: "a", Size: size}}}
	b.SetBytes(size)
	b.ReportAllocs()
	for b.Loop() {
		x, y := mock.Pipe(alice, bob)
		done := make(chan error, 1)
		go func() { done <- e.Send(bg(), x, o, patternSrc{}, nil) }()
		if _, err := e.Receive(bg(), y, func(contract.Offer) bool { return true }, &discardSink{}, nil); err != nil {
			b.Fatal(err)
		}
		if err := <-done; err != nil {
			b.Fatal(err)
		}
	}
}

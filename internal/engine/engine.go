package engine

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/vinayakkam/droplink/internal/contract"
)

// cancelGrace is how long a canceled transfer waits to tell the peer
// before closing the stream anyway.
const cancelGrace = 200 * time.Millisecond

// Engine is safe for concurrent transfers; each call uses its own frames
// and borrows chunk buffers from one shared pool.
type Engine struct {
	chunk int
	pool  *bufPool
}

var _ contract.Engine = (*Engine)(nil)

// New returns an Engine that sends chunks of chunkBytes.
func New(chunkBytes int) *Engine {
	if chunkBytes < 4<<10 {
		chunkBytes = 4 << 10
	}
	return &Engine{chunk: chunkBytes, pool: newBufPool(chunkBytes)}
}

// Haver is optionally implemented by a Sink that keeps partial files. The
// receiver asks it how much of each file it has, and the sender skips that.
type Haver interface {
	Have(f contract.FileMeta) int64
}

type answer struct {
	Accept  bool    `json:"accept"`
	Offsets []int64 `json:"offsets,omitempty"`
}

// CleanPath validates a peer-supplied relative path and returns it in
// canonical form. Absolute paths, "..", backslashes, drive letters and
// empty segments are rejected: the receiver decides where files go.
func CleanPath(p string) (string, error) {
	switch {
	case p == "", len(p) > 1024:
		return "", fmt.Errorf("%w: bad path length", ErrProtocol)
	case strings.ContainsAny(p, "\\\x00:"):
		return "", fmt.Errorf("%w: illegal character in %q", ErrProtocol, p)
	case strings.HasPrefix(p, "/"):
		return "", fmt.Errorf("%w: absolute path %q", ErrProtocol, p)
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return "", fmt.Errorf("%w: bad segment in %q", ErrProtocol, p)
		}
	}
	return path.Clean(p), nil
}

func validateOffer(o *contract.Offer) error {
	if o.ID == "" {
		return fmt.Errorf("%w: offer without id", ErrProtocol)
	}
	if len(o.Files) > maxFiles {
		return fmt.Errorf("%w: %d files", ErrProtocol, len(o.Files))
	}
	seen := make(map[string]struct{}, len(o.Files))
	for i := range o.Files {
		p, err := CleanPath(o.Files[i].Path)
		if err != nil {
			return err
		}
		if o.Files[i].Size < 0 {
			return fmt.Errorf("%w: negative size for %q", ErrProtocol, p)
		}
		key := strings.ToLower(p) // case-insensitive file systems
		if _, dup := seen[key]; dup {
			return fmt.Errorf("%w: duplicate path %q", ErrProtocol, p)
		}
		seen[key] = struct{}{}
		o.Files[i].Path = p
	}
	return nil
}

// session holds per-transfer state shared by Send and Receive.
type session struct {
	ctx    context.Context
	e      *Engine
	c      *conn
	s      contract.Stream
	offer  contract.Offer
	rep    *reporter
	local  *Control // our user's pause
	remote *Control // the peer's pause, as last told
	done   int64
	file   int
}

func (e *Engine) newSession(ctx context.Context, s contract.Stream, progress func(contract.Progress)) *session {
	ss := &session{ctx: ctx, e: e, c: newConn(s), s: s, local: controlFrom(ctx), remote: NewControl()}
	ss.rep = newReporter(progress, "", 0)
	return ss
}

func (ss *session) setOffer(o contract.Offer) {
	ss.offer = o
	ss.rep.id, ss.rep.total = o.ID, o.TotalSize()
}

func (ss *session) state() contract.TransferState {
	if ss.local.Paused() || ss.remote.Paused() {
		return contract.StatePaused
	}
	return contract.StateActive
}

func (ss *session) report(state contract.TransferState, err error, force bool) {
	ss.rep.emit(state, ss.file, ss.done, err, force)
}

// fail tells the peer why we stop (best effort), reports it and returns err.
func (ss *session) fail(err error) error {
	err = ctxErr(ss.ctx, err) // our own cancel, not the closed pipe it caused
	if !errors.Is(err, ErrRemote) && !errors.Is(err, ErrCanceled) {
		_ = ss.c.writeFrame(tError, []byte(err.Error()))
	}
	state := contract.StateFailed
	if errors.Is(err, ErrCanceled) {
		state = contract.StateCancelled
	}
	ss.report(state, err, true)
	return err
}

// remoteErr turns a peer's CANCEL or ERROR frame into an error.
func (ss *session) remoteErr(t byte, n int) error {
	if t == tCancel {
		_ = ss.c.skip(n)
		return ErrCanceled
	}
	msg, _ := ss.c.readControl(min(n, 1024))
	return fmt.Errorf("%w: %s", ErrRemote, msg)
}

// nextFrame reads the next frame header, applying the peer's PAUSE and
// RESUME on the way, and turning CANCEL and ERROR into errors.
func (ss *session) nextFrame() (byte, int, error) {
	for {
		t, n, err := ss.c.readHeader()
		if err != nil {
			return 0, 0, err
		}
		switch t {
		case tPause, tResume:
			if err := ss.c.skip(n); err != nil {
				return 0, 0, err
			}
			if t == tPause {
				ss.remote.Pause()
			} else {
				ss.remote.Resume()
			}
			ss.report(ss.state(), nil, true)
		case tCancel, tError:
			return 0, 0, ss.remoteErr(t, n)
		default:
			return t, n, nil
		}
	}
}

// relayPause tells the peer whenever our user pauses or resumes, until
// stop is closed. It is the only extra goroutine per transfer.
func (ss *session) relayPause(stop <-chan struct{}, wg *sync.WaitGroup) {
	defer wg.Done()
	sent := false
	for {
		paused, changed := ss.local.watch()
		if paused != sent {
			t := tResume
			if paused {
				t = tPause
			}
			if ss.c.writeFrame(t, nil) != nil {
				return
			}
			sent = paused
			ss.report(ss.state(), nil, true)
		}
		select {
		case <-changed:
		case <-stop:
			return
		}
	}
}

// waitRunning blocks while either side is paused.
func (ss *session) waitRunning(ctx context.Context) error {
	for {
		lp, lc := ss.local.watch()
		rp, rc := ss.remote.watch()
		if !lp && !rp {
			return nil
		}
		select {
		case <-ctx.Done():
			return context.Cause(ctx)
		case <-lc:
		case <-rc:
		}
	}
}

// closeOnCancel closes the stream when ctx ends, which unblocks any Read
// or Write in progress. The returned stop must be called.
func closeOnCancel(ctx context.Context, c *conn, s contract.Stream) (stop func() bool) {
	return context.AfterFunc(ctx, func() {
		// Tell the peer, but never wait on a peer that stopped reading.
		done := make(chan struct{})
		go func() { _ = c.writeFrame(tCancel, nil); close(done) }()
		select {
		case <-done:
		case <-time.After(cancelGrace):
		}
		_ = s.Close()
	})
}

// Send offers o and streams its files from src. A Control attached with
// WithControl pauses and resumes it; canceling ctx cancels it on both ends.
func (e *Engine) Send(ctx context.Context, s contract.Stream, o contract.Offer, src contract.Source, progress func(contract.Progress)) (err error) {
	ss := e.newSession(ctx, s, progress)
	defer s.Close()
	stop := closeOnCancel(ctx, ss.c, s)
	defer stop()
	defer func() { err = ctxErr(ctx, err) }()

	if err := validateOffer(&o); err != nil {
		return err
	}
	ss.setOffer(o)
	js, err := json.Marshal(o)
	if err != nil {
		return err
	}
	if err := ss.c.writeFrame(tOffer, js); err != nil {
		return fmt.Errorf("send offer: %w", err)
	}
	ss.report(contract.StateOffered, nil, true)

	ans, err := ss.readAnswer()
	if err != nil {
		return ss.fail(err)
	}
	if !ans.Accept {
		ss.report(contract.StateRejected, ErrRejected, true)
		return ErrRejected
	}
	for _, off := range ans.Offsets {
		ss.done += off
	}
	ss.report(ss.state(), nil, true)

	// From here on the peer may send PAUSE/RESUME/CANCEL/ERROR at any
	// time and finally DONE, so one goroutine reads while this one writes.
	run, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	var wg sync.WaitGroup
	stopRelay := make(chan struct{})
	result := make(chan error, 1)
	wg.Add(2)
	go ss.relayPause(stopRelay, &wg)
	go func() {
		defer wg.Done()
		t, _, err := ss.nextFrame()
		if err == nil && t != tDone {
			err = fmt.Errorf("%w: frame %d while sending", ErrProtocol, t)
		}
		if err != nil {
			cancel(err)
		}
		result <- err
	}()
	defer func() {
		close(stopRelay)
		_ = s.Close() // unblocks the reader if we fail first
		wg.Wait()
	}()

	buf := e.pool.get()
	defer e.pool.put(buf)
	for i, f := range o.Files {
		ss.file = i
		off := int64(0)
		if len(ans.Offsets) > 0 {
			off = ans.Offsets[i]
		}
		if err := ss.sendFile(run, f, off, src, *buf); err != nil {
			return ss.fail(peerReason(err, result))
		}
	}
	if err := ss.c.writeFrame(tDone, nil); err != nil {
		return ss.fail(peerReason(err, result))
	}
	select {
	case err := <-result:
		if err != nil {
			return ss.fail(err)
		}
	case <-run.Done():
		return ss.fail(context.Cause(run))
	}
	ss.file = len(o.Files)
	ss.report(contract.StateDone, nil, true)
	return nil
}

// peerReason prefers the peer's CANCEL or ERROR over the broken pipe it
// causes: a receiver that rejects a file says why, then hangs up, and our
// write may fail before the reader goroutine has seen the message.
func peerReason(err error, result <-chan error) error {
	select {
	case r := <-result:
		if r != nil && (errors.Is(r, ErrRemote) || errors.Is(r, ErrCanceled)) {
			return r
		}
	case <-time.After(cancelGrace):
	}
	return err
}

func (ss *session) readAnswer() (answer, error) {
	var ans answer
	t, n, err := ss.nextFrame()
	if err != nil {
		return ans, fmt.Errorf("wait for answer: %w", err)
	}
	if t != tAnswer {
		return ans, fmt.Errorf("%w: frame %d instead of ANSWER", ErrProtocol, t)
	}
	b, err := ss.c.readControl(n)
	if err != nil {
		return ans, err
	}
	if err := json.Unmarshal(b, &ans); err != nil {
		return ans, fmt.Errorf("%w: answer: %v", ErrProtocol, err)
	}
	if ans.Accept && len(ans.Offsets) != 0 && len(ans.Offsets) != len(ss.offer.Files) {
		return ans, fmt.Errorf("%w: %d offsets for %d files", ErrProtocol, len(ans.Offsets), len(ss.offer.Files))
	}
	for i, off := range ans.Offsets {
		if off < 0 || off > ss.offer.Files[i].Size {
			return ans, fmt.Errorf("%w: offset %d out of range", ErrProtocol, off)
		}
	}
	return ans, nil
}

func (ss *session) sendFile(ctx context.Context, f contract.FileMeta, off int64, src contract.Source, buf []byte) error {
	r, err := src.Open(f)
	if err != nil {
		return fmt.Errorf("open %q: %w", f.Path, err)
	}
	defer r.Close()

	h := sha256.New()
	if off > 0 {
		// The checksum covers the whole file, so hash the bytes the
		// receiver already has without sending them.
		if err := hashPrefix(h, r, off, buf[headerLen:]); err != nil {
			return fmt.Errorf("read %q: %w", f.Path, err)
		}
	}
	chunk := ss.e.chunk
	for remaining := f.Size - off; remaining > 0; {
		if err := ss.waitRunning(ctx); err != nil {
			return err
		}
		n := int(min(int64(chunk), remaining))
		if _, err := io.ReadFull(r, buf[headerLen:headerLen+n]); err != nil {
			return fmt.Errorf("read %q: %w (file changed while sending?)", f.Path, err)
		}
		h.Write(buf[headerLen : headerLen+n])
		if err := ss.c.writeData(buf, n); err != nil {
			return err
		}
		remaining -= int64(n)
		ss.done += int64(n)
		ss.report(contract.StateActive, nil, false)
	}
	var sum [sha256.Size]byte
	return ss.c.writeFrame(tFileEnd, h.Sum(sum[:0]))
}

// hashPrefix feeds the first n bytes of r into h using buf.
func hashPrefix(h hash.Hash, r io.Reader, n int64, buf []byte) error {
	for n > 0 {
		k := int(min(int64(len(buf)), n))
		if _, err := io.ReadFull(r, buf[:k]); err != nil {
			return err
		}
		h.Write(buf[:k])
		n -= int64(k)
	}
	return nil
}

// Receive reads an offer, asks accept, and stores the files into dst. If
// dst implements Haver, files it already partly has resume where they
// stopped.
func (e *Engine) Receive(ctx context.Context, s contract.Stream, accept func(contract.Offer) bool, dst contract.Sink, progress func(contract.Progress)) (o contract.Offer, err error) {
	ss := e.newSession(ctx, s, progress)
	defer s.Close()
	stop := closeOnCancel(ctx, ss.c, s)
	defer stop()
	defer func() { err = ctxErr(ctx, err) }()

	t, n, err := ss.nextFrame()
	if err != nil {
		return o, fmt.Errorf("wait for offer: %w", err)
	}
	if t != tOffer {
		return o, fmt.Errorf("%w: frame %d instead of OFFER", ErrProtocol, t)
	}
	b, err := ss.c.readControl(n)
	if err != nil {
		return o, err
	}
	if err := json.Unmarshal(b, &o); err != nil {
		return o, ss.fail(fmt.Errorf("%w: offer: %v", ErrProtocol, err))
	}
	if err := validateOffer(&o); err != nil {
		return o, ss.fail(err)
	}
	ss.setOffer(o)
	ss.report(contract.StateOffered, nil, true)

	if !accept(o) {
		js, _ := json.Marshal(answer{Accept: false})
		_ = ss.c.writeFrame(tAnswer, js)
		ss.report(contract.StateRejected, ErrRejected, true)
		return o, ErrRejected
	}
	ans := answer{Accept: true}
	if hv, ok := dst.(Haver); ok {
		ans.Offsets = make([]int64, len(o.Files))
		for i, f := range o.Files {
			ans.Offsets[i] = min(max(hv.Have(f), 0), f.Size)
			ss.done += ans.Offsets[i]
		}
	}
	js, _ := json.Marshal(ans)
	if err := ss.c.writeFrame(tAnswer, js); err != nil {
		return o, ss.fail(err)
	}
	ss.report(ss.state(), nil, true)

	var wg sync.WaitGroup
	stopRelay := make(chan struct{})
	wg.Add(1)
	go ss.relayPause(stopRelay, &wg)
	defer func() {
		close(stopRelay)
		wg.Wait()
	}()

	buf := e.pool.get()
	defer e.pool.put(buf)
	for i, f := range o.Files {
		ss.file = i
		off := int64(0)
		if ans.Offsets != nil {
			off = ans.Offsets[i]
		}
		if err := ss.recvFile(f, off, dst, *buf); err != nil {
			return o, ss.fail(err)
		}
	}

	t, _, err = ss.nextFrame()
	if err != nil {
		return o, ss.fail(err)
	}
	if t != tDone {
		return o, ss.fail(fmt.Errorf("%w: frame %d instead of DONE", ErrProtocol, t))
	}
	if err := ss.c.writeFrame(tDone, nil); err != nil {
		return o, ss.fail(err)
	}
	ss.file = len(o.Files)
	ss.report(contract.StateDone, nil, true)
	return o, nil
}

func (ss *session) recvFile(f contract.FileMeta, off int64, dst contract.Sink, buf []byte) error {
	w, have, err := dst.Open(f)
	if err != nil {
		return fmt.Errorf("store %q: %w", f.Path, err)
	}
	closed := false
	defer func() {
		if !closed {
			_ = w.Close()
		}
	}()
	if have != off {
		return fmt.Errorf("%w: sink has %d bytes of %q, answered %d", ErrProtocol, have, f.Path, off)
	}

	h := sha256.New()
	if have > 0 {
		ra, ok := w.(io.ReaderAt)
		if !ok {
			return fmt.Errorf("store %q: sink cannot be read back to resume", f.Path)
		}
		if err := hashPrefix(h, io.NewSectionReader(ra, 0, have), have, buf); err != nil {
			return fmt.Errorf("rehash %q: %w", f.Path, err)
		}
	}
	got := have
	for {
		t, n, err := ss.nextFrame()
		if err != nil {
			return err
		}
		switch t {
		case tData:
			if n > ss.e.chunk || got+int64(n) > f.Size {
				return fmt.Errorf("%w: %d data bytes past %q", ErrProtocol, n, f.Path)
			}
			p := buf[:n]
			if _, err := io.ReadFull(ss.c.rw, p); err != nil {
				return err
			}
			h.Write(p)
			if _, err := w.Write(p); err != nil {
				return fmt.Errorf("write %q: %w", f.Path, err)
			}
			got += int64(n)
			ss.done += int64(n)
			ss.report(ss.state(), nil, false)
		case tFileEnd:
			if n != sha256.Size || got != f.Size {
				return fmt.Errorf("%w: %q ended at %d of %d bytes", ErrProtocol, f.Path, got, f.Size)
			}
			var want, sum [sha256.Size]byte
			if _, err := io.ReadFull(ss.c.rw, want[:]); err != nil {
				return err
			}
			closed = true
			if err := w.Close(); err != nil {
				return fmt.Errorf("close %q: %w", f.Path, err)
			}
			if !bytes.Equal(h.Sum(sum[:0]), want[:]) {
				_ = dst.Abort(f, ErrCorrupt)
				return fmt.Errorf("%w: %q", ErrCorrupt, f.Path)
			}
			return dst.Commit(f)
		default:
			return fmt.Errorf("%w: frame %d inside %q", ErrProtocol, t, f.Path)
		}
	}
}

// ctxErr reports cancellation instead of the I/O error it caused.
func ctxErr(ctx context.Context, err error) error {
	if err != nil && ctx.Err() != nil && !errors.Is(err, ErrRejected) && !errors.Is(err, ErrCanceled) {
		return fmt.Errorf("%w: %w", ErrCanceled, ctx.Err())
	}
	return err
}

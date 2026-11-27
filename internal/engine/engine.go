package engine

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
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
	e        *Engine
	c        *conn
	s        contract.Stream
	offer    contract.Offer
	progress func(contract.Progress)
	done     int64
	total    int64
}

func (ss *session) report(state contract.TransferState, file int, err error) {
	if ss.progress == nil {
		return
	}
	p := contract.Progress{ID: ss.offer.ID, State: state, File: file, Done: ss.done, Total: ss.total}
	if err != nil {
		p.Err = err.Error()
	}
	ss.progress(p)
}

// fail tells the peer why we stop (best effort) and returns err.
func (ss *session) fail(err error) error {
	if !errors.Is(err, ErrRemote) && !errors.Is(err, ErrCanceled) {
		_ = ss.c.writeFrame(tError, []byte(err.Error()))
	}
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

// Send offers o and streams its files from src.
func (e *Engine) Send(ctx context.Context, s contract.Stream, o contract.Offer, src contract.Source, progress func(contract.Progress)) (err error) {
	ss := &session{e: e, c: newConn(s), s: s, offer: o, progress: progress, total: o.TotalSize()}
	defer s.Close()
	stop := closeOnCancel(ctx, ss.c, s)
	defer stop()
	defer func() { err = ctxErr(ctx, err) }()

	if err := validateOffer(&ss.offer); err != nil {
		return err
	}
	js, err := json.Marshal(ss.offer)
	if err != nil {
		return err
	}
	if err := ss.c.writeFrame(tOffer, js); err != nil {
		return fmt.Errorf("send offer: %w", err)
	}
	ss.report(contract.StateOffered, 0, nil)

	ans, err := ss.readAnswer()
	if err != nil {
		return err
	}
	if !ans.Accept {
		ss.report(contract.StateRejected, 0, ErrRejected)
		return ErrRejected
	}
	ss.report(contract.StateActive, 0, nil)

	buf := e.pool.get()
	defer e.pool.put(buf)
	for i, f := range ss.offer.Files {
		if err := ss.sendFile(i, f, src, *buf); err != nil {
			ss.report(contract.StateFailed, i, err)
			return ss.fail(err)
		}
	}
	if err := ss.c.writeFrame(tDone, nil); err != nil {
		return err
	}
	// Wait for the receiver to confirm every file is committed.
	t, n, err := ss.c.readHeader()
	if err != nil {
		return fmt.Errorf("wait for done: %w", err)
	}
	if t != tDone {
		if t == tCancel || t == tError {
			return ss.remoteErr(t, n)
		}
		return fmt.Errorf("%w: frame %d instead of DONE", ErrProtocol, t)
	}
	ss.report(contract.StateDone, len(ss.offer.Files), nil)
	return nil
}

func (ss *session) readAnswer() (answer, error) {
	var ans answer
	t, n, err := ss.c.readHeader()
	if err != nil {
		return ans, fmt.Errorf("wait for answer: %w", err)
	}
	if t == tCancel || t == tError {
		return ans, ss.remoteErr(t, n)
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

func (ss *session) sendFile(i int, f contract.FileMeta, src contract.Source, buf []byte) error {
	r, err := src.Open(f)
	if err != nil {
		return fmt.Errorf("open %q: %w", f.Path, err)
	}
	defer r.Close()

	h := sha256.New()
	chunk := ss.e.chunk
	for remaining := f.Size; remaining > 0; {
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
		ss.report(contract.StateActive, i, nil)
	}
	var sum [sha256.Size]byte
	return ss.c.writeFrame(tFileEnd, h.Sum(sum[:0]))
}

// Receive reads an offer, asks accept, and stores the files into dst.
func (e *Engine) Receive(ctx context.Context, s contract.Stream, accept func(contract.Offer) bool, dst contract.Sink, progress func(contract.Progress)) (o contract.Offer, err error) {
	ss := &session{e: e, c: newConn(s), s: s, progress: progress}
	defer s.Close()
	stop := closeOnCancel(ctx, ss.c, s)
	defer stop()
	defer func() { err = ctxErr(ctx, err) }()

	t, n, err := ss.c.readHeader()
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
	if err := json.Unmarshal(b, &ss.offer); err != nil {
		return o, ss.fail(fmt.Errorf("%w: offer: %v", ErrProtocol, err))
	}
	if err := validateOffer(&ss.offer); err != nil {
		return ss.offer, ss.fail(err)
	}
	o = ss.offer
	ss.total = o.TotalSize()
	ss.report(contract.StateOffered, 0, nil)

	if !accept(o) {
		js, _ := json.Marshal(answer{Accept: false})
		_ = ss.c.writeFrame(tAnswer, js)
		ss.report(contract.StateRejected, 0, ErrRejected)
		return o, ErrRejected
	}
	js, _ := json.Marshal(answer{Accept: true})
	if err := ss.c.writeFrame(tAnswer, js); err != nil {
		return o, err
	}
	ss.report(contract.StateActive, 0, nil)

	buf := e.pool.get()
	defer e.pool.put(buf)
	for i, f := range o.Files {
		if err := ss.recvFile(i, f, dst, *buf); err != nil {
			ss.report(contract.StateFailed, i, err)
			return o, ss.fail(err)
		}
	}

	t, n, err = ss.c.readHeader()
	if err != nil {
		return o, err
	}
	if t != tDone {
		if t == tCancel || t == tError {
			return o, ss.remoteErr(t, n)
		}
		return o, ss.fail(fmt.Errorf("%w: frame %d instead of DONE", ErrProtocol, t))
	}
	if err := ss.c.writeFrame(tDone, nil); err != nil {
		return o, err
	}
	ss.report(contract.StateDone, len(o.Files), nil)
	return o, nil
}

func (ss *session) recvFile(i int, f contract.FileMeta, dst contract.Sink, buf []byte) error {
	w, _, err := dst.Open(f)
	if err != nil {
		return fmt.Errorf("store %q: %w", f.Path, err)
	}
	closed := false
	defer func() {
		if !closed {
			_ = w.Close()
		}
	}()

	h := sha256.New()
	var got int64
	for {
		t, n, err := ss.c.readHeader()
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
			ss.report(contract.StateActive, i, nil)
		case tFileEnd:
			if n != sha256.Size || got != f.Size {
				return fmt.Errorf("%w: %q ended at %d of %d bytes", ErrProtocol, f.Path, got, f.Size)
			}
			var want, have [sha256.Size]byte
			if _, err := io.ReadFull(ss.c.rw, want[:]); err != nil {
				return err
			}
			closed = true
			if err := w.Close(); err != nil {
				return fmt.Errorf("close %q: %w", f.Path, err)
			}
			if !bytes.Equal(h.Sum(have[:0]), want[:]) {
				_ = dst.Abort(f, ErrCorrupt)
				return fmt.Errorf("%w: %q", ErrCorrupt, f.Path)
			}
			return dst.Commit(f)
		case tCancel, tError:
			return ss.remoteErr(t, n)
		default:
			return fmt.Errorf("%w: frame %d inside %q", ErrProtocol, t, f.Path)
		}
	}
}

// ctxErr reports cancellation instead of the I/O error it caused.
func ctxErr(ctx context.Context, err error) error {
	if err != nil && ctx.Err() != nil && !errors.Is(err, ErrRejected) {
		return fmt.Errorf("%w: %w", ErrCanceled, ctx.Err())
	}
	return err
}

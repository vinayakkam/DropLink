package lan

import (
	"fmt"
	"io"
	"net"
	"sync"

	"github.com/klauspost/compress/zstd"
)

// Feature flags exchanged in one byte right after the handshake: the
// dialer proposes, the listener answers with what both support.
const (
	flagZstd   byte = 1 << 0
	knownFlags      = flagZstd
)

// window bounds zstd memory per stream (encoder and decoder).
const window = 1 << 20

// negotiate exchanges feature flags over the secured conn.
func negotiate(c net.Conn, initiator bool, want byte) (byte, error) {
	var b [1]byte
	if initiator {
		b[0] = want
		if _, err := c.Write(b[:]); err != nil {
			return 0, err
		}
		if _, err := io.ReadFull(c, b[:]); err != nil {
			return 0, err
		}
		if b[0]&^want != 0 {
			return 0, fmt.Errorf("lan: peer enabled features we did not offer (%#x)", b[0])
		}
		return b[0], nil
	}
	if _, err := io.ReadFull(c, b[:]); err != nil {
		return 0, err
	}
	agreed := b[0] & want & knownFlags
	b[0] = agreed
	if _, err := c.Write(b[:]); err != nil {
		return 0, err
	}
	return agreed, nil
}

// flushWriter compresses each Write and flushes it at once: the engine
// waits for answers, so nothing may sit in the encoder's buffer. mu lets
// Close finish the frame without racing a concurrent Write.
type flushWriter struct {
	mu  *sync.Mutex
	enc *zstd.Encoder
}

func (f flushWriter) Write(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	n, err := f.enc.Write(p)
	if err != nil {
		return n, err
	}
	return n, f.enc.Flush()
}

// lockedReader serializes Reads with freeing the decoder: Close closes
// the conn first (which ends a blocked Read), then frees under the lock.
type lockedReader struct {
	mu  *sync.Mutex
	dec *zstd.Decoder
}

func (l *lockedReader) Read(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.dec == nil {
		return 0, net.ErrClosed
	}
	return l.dec.Read(p)
}

type closerFunc func() error

func (f closerFunc) Close() error { return f() }

// compress wraps s in zstd both ways. Fastest level, one goroutine and a
// 1 MiB window keep it to a few MiB per stream. Data that does not
// compress (zip, jpg, mp4) is stored raw by zstd, at little cost.
func compress(s *stream) error {
	enc, err := zstd.NewWriter(s.conn,
		zstd.WithEncoderLevel(zstd.SpeedFastest),
		zstd.WithEncoderConcurrency(1),
		zstd.WithWindowSize(window),
		zstd.WithLowerEncoderMem(true))
	if err != nil {
		return err
	}
	dec, err := zstd.NewReader(s.conn,
		zstd.WithDecoderConcurrency(1),
		zstd.WithDecoderMaxWindow(window),
		zstd.WithDecoderLowmem(true))
	if err != nil {
		enc.Close()
		return err
	}
	mu, rmu := &sync.Mutex{}, &sync.Mutex{}
	lr := &lockedReader{mu: rmu, dec: dec}
	s.r, s.w = lr, flushWriter{mu: mu, enc: enc}
	// Before the conn closes: end the zstd frame so the peer reads a clean
	// EOF. Close has already set a short write deadline, so a stuck peer
	// cannot hold this up.
	s.finish = func() {
		mu.Lock()
		_ = enc.Close()
		mu.Unlock()
	}
	s.extra = append(s.extra, closerFunc(func() error {
		rmu.Lock()
		defer rmu.Unlock()
		lr.dec.Close()
		lr.dec = nil
		return nil
	}))
	return nil
}

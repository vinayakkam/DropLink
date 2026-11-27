package lan

import (
	"fmt"
	"io"
	"net"

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
// waits for answers, so nothing may sit in the encoder's buffer.
type flushWriter struct{ enc *zstd.Encoder }

func (f flushWriter) Write(p []byte) (int, error) {
	n, err := f.enc.Write(p)
	if err != nil {
		return n, err
	}
	return n, f.enc.Flush()
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
	s.r, s.w = dec, flushWriter{enc}
	s.extra = append(s.extra,
		closerFunc(func() error { _ = enc.Close(); return nil }), // conn is closed; just free state
		closerFunc(func() error { dec.Close(); return nil }))
	return nil
}

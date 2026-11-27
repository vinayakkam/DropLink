// Package engine implements contract.Engine: it moves an Offer's files over
// one contract.Stream in fixed-size chunks, with constant memory.
//
// Wire format, both directions: every frame is
//
//	type u8 | length u32 (big endian) | payload
//
// Data frames carry at most one chunk. Control frames carry JSON or a few
// bytes and are capped at maxControl. A conversation looks like:
//
//	sender                          receiver
//	OFFER {offer json}        →
//	                          ←     ANSWER {accept, offsets}
//	DATA ... DATA, FILE_END   →     (per file; FILE_END has the SHA-256)
//	DONE                      →
//	                          ←     DONE   (all files committed)
//
// PAUSE, RESUME, CANCEL and ERROR may go either way at any time.
//
// The engine owns the Stream it is given and closes it before returning.
package engine

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sync"
)

// Frame types.
const (
	tOffer   byte = 1
	tAnswer  byte = 2
	tData    byte = 3
	tFileEnd byte = 4
	tDone    byte = 5
	tPause   byte = 6
	tResume  byte = 7
	tCancel  byte = 8
	tError   byte = 9
)

const (
	headerLen  = 5
	maxControl = 4 << 20 // an offer with ~100k paths still fits
	maxFiles   = 100_000
)

// Errors the engine returns. Wrapped errors keep these at the root.
var (
	ErrRejected = errors.New("engine: offer rejected")
	ErrCanceled = errors.New("engine: transfer canceled")
	ErrCorrupt  = errors.New("engine: checksum mismatch")
	ErrProtocol = errors.New("engine: protocol error")
	ErrRemote   = errors.New("engine: peer reported an error")
)

// conn frames one Stream. Reads happen on one goroutine only; writes are
// serialized by wmu because control frames can come from a second one.
type conn struct {
	rw   io.ReadWriter
	rhdr [headerLen]byte
	wmu  sync.Mutex
	whdr [headerLen]byte
}

func newConn(rw io.ReadWriter) *conn { return &conn{rw: rw} }

// writeFrame writes a control frame (header and payload as two writes).
func (c *conn) writeFrame(t byte, payload []byte) error {
	if len(payload) > maxControl {
		return fmt.Errorf("%w: control frame of %d bytes", ErrProtocol, len(payload))
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	c.whdr[0] = t
	binary.BigEndian.PutUint32(c.whdr[1:], uint32(len(payload)))
	if _, err := c.rw.Write(c.whdr[:]); err != nil {
		return err
	}
	if len(payload) == 0 {
		return nil
	}
	_, err := c.rw.Write(payload)
	return err
}

// writeData sends buf[headerLen:headerLen+n] as one DATA frame. The header
// is written into buf's first bytes so the frame goes out in a single Write
// with no copy and no allocation.
func (c *conn) writeData(buf []byte, n int) error {
	buf[0] = tData
	binary.BigEndian.PutUint32(buf[1:headerLen], uint32(n))
	c.wmu.Lock()
	defer c.wmu.Unlock()
	_, err := c.rw.Write(buf[:headerLen+n])
	return err
}

// readHeader reads the next frame header.
func (c *conn) readHeader() (t byte, n int, err error) {
	if _, err := io.ReadFull(c.rw, c.rhdr[:]); err != nil {
		return 0, 0, err
	}
	return c.rhdr[0], int(binary.BigEndian.Uint32(c.rhdr[1:])), nil
}

// readControl reads a control payload of n bytes.
func (c *conn) readControl(n int) ([]byte, error) {
	if n > maxControl {
		return nil, fmt.Errorf("%w: control frame of %d bytes", ErrProtocol, n)
	}
	b := make([]byte, n)
	_, err := io.ReadFull(c.rw, b)
	return b, err
}

// skip discards n payload bytes of a frame we do not care about.
func (c *conn) skip(n int) error {
	if n > maxControl {
		return fmt.Errorf("%w: frame of %d bytes", ErrProtocol, n)
	}
	_, err := io.CopyN(io.Discard, c.rw, int64(n))
	return err
}

// bufPool hands out chunk buffers with headerLen bytes of headroom.
type bufPool struct {
	size int
	p    sync.Pool
}

func newBufPool(chunk int) *bufPool {
	bp := &bufPool{size: headerLen + chunk}
	bp.p.New = func() any { b := make([]byte, bp.size); return &b }
	return bp
}

func (bp *bufPool) get() *[]byte  { return bp.p.Get().(*[]byte) }
func (bp *bufPool) put(b *[]byte) { bp.p.Put(b) }

package crypto

import (
	"context"
	"crypto/cipher"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"golang.org/x/crypto/chacha20poly1305"

	"github.com/vinayakkam/droplink/internal/contract"
)

// MaxRecord is the largest plaintext in one record.
const MaxRecord = 16 << 10

// Record: len u16 (ciphertext incl. tag) | ChaCha20-Poly1305 ciphertext.
// The nonce is a per-direction counter, never sent: a replayed, dropped
// or reordered record fails authentication.
const (
	recHeader = 2
	overhead  = chacha20poly1305.Overhead
)

// ErrRecord is returned when a record fails to authenticate.
var ErrRecord = errors.New("crypto: record authentication failed")

// Conn is an encrypted, authenticated net.Conn (contract.SecureConn).
type Conn struct {
	net.Conn
	sess *Session

	rmu     sync.Mutex
	raead   cipher.AEAD
	rseq    uint64
	rbuf    []byte // one ciphertext record
	pending []byte // decrypted bytes not yet returned (slice of rbuf)
	rerr    error  // sticky: after a bad record the stream is dead

	wmu   sync.Mutex
	waead cipher.AEAD
	wseq  uint64
	wbuf  []byte
}

var _ contract.SecureConn = (*Conn)(nil)

func newConn(c net.Conn, s *Session) (*Conn, error) {
	ra, err := chacha20poly1305.New(s.RecvKey[:])
	if err != nil {
		return nil, err
	}
	wa, err := chacha20poly1305.New(s.SendKey[:])
	if err != nil {
		return nil, err
	}
	return &Conn{
		Conn: c, sess: s,
		raead: ra, rbuf: make([]byte, recHeader+MaxRecord+overhead),
		waead: wa, wbuf: make([]byte, recHeader+MaxRecord+overhead),
	}, nil
}

// RemoteID is the authenticated peer's device ID.
func (c *Conn) RemoteID() contract.DeviceID { return c.sess.RemoteID() }

// SAS is the 4-glyph code both screens show.
func (c *Conn) SAS() contract.SAS { return c.sess.SAS }

func nonce(dst *[chacha20poly1305.NonceSize]byte, seq uint64) []byte {
	binary.BigEndian.PutUint64(dst[4:], seq)
	return dst[:]
}

// Write encrypts p in records of at most MaxRecord bytes.
func (c *Conn) Write(p []byte) (int, error) {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	var nb [chacha20poly1305.NonceSize]byte
	written := 0
	for len(p) > 0 {
		if c.wseq == ^uint64(0) {
			return written, errors.New("crypto: nonce space exhausted; reconnect")
		}
		n := min(len(p), MaxRecord)
		body := c.wbuf[recHeader : recHeader+n]
		copy(body, p[:n])
		ct := c.waead.Seal(body[:0], nonce(&nb, c.wseq), body, nil)
		c.wseq++
		binary.BigEndian.PutUint16(c.wbuf[:recHeader], uint16(len(ct)))
		if _, err := c.Conn.Write(c.wbuf[:recHeader+len(ct)]); err != nil {
			return written, err
		}
		written += n
		p = p[n:]
	}
	return written, nil
}

// Read returns decrypted bytes, reading one record at a time.
func (c *Conn) Read(p []byte) (int, error) {
	c.rmu.Lock()
	defer c.rmu.Unlock()
	if len(c.pending) == 0 {
		if c.rerr != nil {
			return 0, c.rerr
		}
		if err := c.readRecord(); err != nil {
			return 0, err
		}
	}
	n := copy(p, c.pending)
	c.pending = c.pending[n:]
	return n, nil
}

func (c *Conn) readRecord() error {
	for {
		if _, err := readFull(c.Conn, c.rbuf[:recHeader]); err != nil {
			return err
		}
		n := int(binary.BigEndian.Uint16(c.rbuf[:recHeader]))
		if n < overhead || n > MaxRecord+overhead {
			c.rerr = fmt.Errorf("%w: record of %d bytes", ErrRecord, n)
			return c.rerr
		}
		ct := c.rbuf[recHeader : recHeader+n]
		if _, err := readFull(c.Conn, ct); err != nil {
			return err
		}
		var nb [chacha20poly1305.NonceSize]byte
		pt, err := c.raead.Open(ct[:0], nonce(&nb, c.rseq), ct, nil)
		if err != nil {
			c.rerr = ErrRecord
			return c.rerr
		}
		c.rseq++
		if len(pt) > 0 {
			c.pending = pt
			return nil
		}
		// An empty record carries nothing; read the next one.
	}
}

func readFull(c net.Conn, b []byte) (int, error) {
	n := 0
	for n < len(b) {
		k, err := c.Read(b[n:])
		n += k
		if err != nil {
			return n, err
		}
	}
	return n, nil
}

// Crypto implements contract.Crypto for this device.
type Crypto struct {
	id    *Identity
	trust *trustStore
	// HandshakeTimeout bounds Secure when ctx has no deadline, so a
	// silent peer cannot hold a goroutine forever.
	HandshakeTimeout time.Duration
}

// ID returns this device's ID.
func (c *Crypto) ID() contract.DeviceID { return c.id.ID() }

// Identity returns the key pair.
func (c *Crypto) Identity() *Identity { return c.id }

// Secure runs the handshake over conn and returns the encrypted Conn.
// On failure conn is left open; the caller closes it.
func (c *Crypto) Secure(ctx context.Context, conn net.Conn, initiator bool) (contract.SecureConn, error) {
	deadline, ok := ctx.Deadline()
	if !ok {
		t := c.HandshakeTimeout
		if t <= 0 {
			t = 10 * time.Second
		}
		deadline = time.Now().Add(t)
	}
	if err := conn.SetDeadline(deadline); err != nil {
		return nil, err
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.SetDeadline(time.Unix(1, 0)) })
	sess, err := Handshake(conn, c.id, initiator)
	stop()
	if err != nil {
		return nil, err
	}
	if err := conn.SetDeadline(time.Time{}); err != nil {
		return nil, err
	}
	return newConn(conn, sess)
}

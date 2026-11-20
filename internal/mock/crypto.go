package mock

import (
	"context"
	"crypto/sha256"
	"net"
	"slices"
	"strings"
	"sync"

	"github.com/vinayakkam/droplink/internal/contract"
)

// Crypto is a pass-through: Secure returns the same bytes unencrypted and
// a deterministic SAS, so both ends of a test agree without a handshake.
// Never use it outside tests and the UI demo.
type Crypto struct {
	id      contract.DeviceID
	mu      sync.Mutex
	trusted map[contract.DeviceID]contract.TrustedDevice
}

// NewCrypto returns a Crypto with the given identity.
func NewCrypto(id contract.DeviceID) *Crypto {
	return &Crypto{id: id, trusted: map[contract.DeviceID]contract.TrustedDevice{}}
}

func (c *Crypto) ID() contract.DeviceID { return c.id }

// Secure exchanges IDs (one line each way) and wraps conn.
func (c *Crypto) Secure(ctx context.Context, conn net.Conn, initiator bool) (contract.SecureConn, error) {
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
		defer conn.SetDeadline(timeZero)
	}
	send := func() error { _, err := conn.Write([]byte(string(c.id) + "\n")); return err }
	recv := func() (contract.DeviceID, error) {
		var b []byte
		one := make([]byte, 1)
		for len(b) < 128 {
			if _, err := conn.Read(one); err != nil {
				return "", err
			}
			if one[0] == '\n' {
				break
			}
			b = append(b, one[0])
		}
		return contract.DeviceID(b), nil
	}
	var remote contract.DeviceID
	var err error
	// The initiator writes first so a synchronous pipe cannot deadlock.
	if initiator {
		if err = send(); err == nil {
			remote, err = recv()
		}
	} else {
		if remote, err = recv(); err == nil {
			err = send()
		}
	}
	if err != nil {
		return nil, err
	}
	return &secureConn{Conn: conn, remote: remote, sas: SASFor(c.id, remote)}, nil
}

func (c *Crypto) Trust(d contract.TrustedDevice) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.trusted[d.ID] = d
	return nil
}

func (c *Crypto) Untrust(id contract.DeviceID) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.trusted, id)
	return nil
}

func (c *Crypto) Trusted() []contract.TrustedDevice {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]contract.TrustedDevice, 0, len(c.trusted))
	for _, d := range c.trusted {
		out = append(out, d)
	}
	slices.SortFunc(out, func(a, b contract.TrustedDevice) int { return strings.Compare(string(a.ID), string(b.ID)) })
	return out
}

func (c *Crypto) IsTrusted(id contract.DeviceID) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.trusted[id]
	return ok
}

// SASFor derives the same 4 glyphs on both ends regardless of order.
func SASFor(a, b contract.DeviceID) contract.SAS {
	if a > b {
		a, b = b, a
	}
	sum := sha256.Sum256([]byte(string(a) + "|" + string(b)))
	return contract.SAS{sum[0] & 63, sum[1] & 63, sum[2] & 63, sum[3] & 63}
}

type secureConn struct {
	net.Conn
	remote contract.DeviceID
	sas    contract.SAS
}

func (s *secureConn) RemoteID() contract.DeviceID { return s.remote }
func (s *secureConn) SAS() contract.SAS           { return s.sas }

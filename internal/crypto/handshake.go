package crypto

import (
	"bytes"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"

	"github.com/vinayakkam/droplink/internal/contract"
)

// Handshake messages (sizes are fixed, so no length prefixes):
//
//	1  I→R  "DLH1" | eph_I (32) | static_I (32)
//	2  R→I  eph_R (32) | static_R (32) | sig_R (64)
//	3  I→R  sig_I (64)
//
// T = msg1 | eph_R | static_R, h = SHA-256(label | T).
// sig_R = Sign(R, "resp" | h), sig_I = Sign(I, "init" | h): each side
// proves it holds the static key it claimed, over both ephemeral keys,
// so neither message can be replayed into another session.
// Keys: HKDF-SHA256(X25519(eph_I, eph_R), salt h) with one key per
// direction and 4 bytes for the SAS.
const (
	magic   = "DLH1"
	label   = "droplink handshake v1"
	msg1Len = 4 + 32 + ed25519.PublicKeySize
	msg2Len = 32 + ed25519.PublicKeySize + ed25519.SignatureSize
	msg3Len = ed25519.SignatureSize
)

// ErrHandshake wraps every handshake failure.
var ErrHandshake = errors.New("crypto: handshake failed")

// Session is the result of a handshake.
type Session struct {
	SendKey, RecvKey [32]byte
	Remote           ed25519.PublicKey
	SAS              contract.SAS
}

// RemoteID is the peer's device ID.
func (s *Session) RemoteID() contract.DeviceID { return IDFor(s.Remote) }

// Handshake authenticates both ends over rw. The caller sets deadlines.
func Handshake(rw io.ReadWriter, id *Identity, initiator bool) (*Session, error) {
	eph, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	ephPub := eph.PublicKey().Bytes()

	var msg1 [msg1Len]byte
	var ephR, staticR []byte
	var sigR []byte
	var remote ed25519.PublicKey
	var remoteEph []byte

	if initiator {
		copy(msg1[:], magic)
		copy(msg1[4:], ephPub)
		copy(msg1[36:], id.pub)
		if _, err := rw.Write(msg1[:]); err != nil {
			return nil, fmt.Errorf("%w: send hello: %w", ErrHandshake, err)
		}
		var msg2 [msg2Len]byte
		if _, err := io.ReadFull(rw, msg2[:]); err != nil {
			return nil, fmt.Errorf("%w: read reply: %w", ErrHandshake, err)
		}
		ephR, staticR, sigR = msg2[:32], msg2[32:64], msg2[64:]
		remote, remoteEph = ed25519.PublicKey(bytes.Clone(staticR)), ephR
	} else {
		if _, err := io.ReadFull(rw, msg1[:]); err != nil {
			return nil, fmt.Errorf("%w: read hello: %w", ErrHandshake, err)
		}
		if string(msg1[:4]) != magic {
			return nil, fmt.Errorf("%w: not a DropLink peer", ErrHandshake)
		}
		ephR, staticR = ephPub, id.pub
		remote, remoteEph = ed25519.PublicKey(bytes.Clone(msg1[36:])), msg1[4:36]
	}
	if bytes.Equal(remote, id.pub) {
		return nil, fmt.Errorf("%w: connected to ourselves", ErrHandshake)
	}

	h := transcriptHash(msg1[:], ephR, staticR)

	if initiator {
		if !ed25519.Verify(remote, signed("resp", h), sigR) {
			return nil, fmt.Errorf("%w: responder signature invalid", ErrHandshake)
		}
		if _, err := rw.Write(ed25519.Sign(id.priv, signed("init", h))); err != nil {
			return nil, fmt.Errorf("%w: send proof: %w", ErrHandshake, err)
		}
	} else {
		var msg2 [msg2Len]byte
		copy(msg2[:], ephPub)
		copy(msg2[32:], id.pub)
		copy(msg2[64:], ed25519.Sign(id.priv, signed("resp", h)))
		if _, err := rw.Write(msg2[:]); err != nil {
			return nil, fmt.Errorf("%w: send reply: %w", ErrHandshake, err)
		}
		var msg3 [msg3Len]byte
		if _, err := io.ReadFull(rw, msg3[:]); err != nil {
			return nil, fmt.Errorf("%w: read proof: %w", ErrHandshake, err)
		}
		if !ed25519.Verify(remote, signed("init", h), msg3[:]) {
			return nil, fmt.Errorf("%w: initiator signature invalid", ErrHandshake)
		}
	}

	peerEph, err := ecdh.X25519().NewPublicKey(remoteEph)
	if err != nil {
		return nil, fmt.Errorf("%w: bad ephemeral key: %w", ErrHandshake, err)
	}
	shared, err := eph.ECDH(peerEph) // rejects low-order points
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrHandshake, err)
	}
	return deriveSession(shared, h, remote, initiator)
}

func transcriptHash(msg1, ephR, staticR []byte) []byte {
	d := sha256.New()
	d.Write([]byte(label))
	d.Write(msg1)
	d.Write(ephR)
	d.Write(staticR)
	return d.Sum(nil)
}

func signed(role string, h []byte) []byte {
	return append([]byte("droplink "+role+" "), h...)
}

func deriveSession(shared, h []byte, remote ed25519.PublicKey, initiator bool) (*Session, error) {
	i2r, err := hkdf.Key(sha256.New, shared, h, "droplink i2r", 32)
	if err != nil {
		return nil, err
	}
	r2i, err := hkdf.Key(sha256.New, shared, h, "droplink r2i", 32)
	if err != nil {
		return nil, err
	}
	sas, err := hkdf.Key(sha256.New, shared, h, "droplink sas", 4)
	if err != nil {
		return nil, err
	}
	s := &Session{Remote: remote}
	if initiator {
		copy(s.SendKey[:], i2r)
		copy(s.RecvKey[:], r2i)
	} else {
		copy(s.SendKey[:], r2i)
		copy(s.RecvKey[:], i2r)
	}
	for i := range s.SAS {
		s.SAS[i] = sas[i] & 63
	}
	return s, nil
}

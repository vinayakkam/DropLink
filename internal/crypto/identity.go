// Package crypto implements contract.Crypto: each device has an Ed25519
// identity key; every LAN connection runs an authenticated X25519
// handshake and then ChaCha20-Poly1305 records; both people compare a
// 4-glyph SAS; and trusted devices are remembered.
package crypto

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/vinayakkam/droplink/internal/contract"
)

// KeyFile holds the 32-byte Ed25519 seed. It never leaves the device.
const KeyFile = "identity.key"

// Identity is this device's long-term key pair.
type Identity struct {
	priv ed25519.PrivateKey
	pub  ed25519.PublicKey
	id   contract.DeviceID
}

// IDFor derives a DeviceID from a public key: hex of the first 16 bytes
// of its SHA-256.
func IDFor(pub ed25519.PublicKey) contract.DeviceID {
	sum := sha256.Sum256(pub)
	return contract.DeviceID(hex.EncodeToString(sum[:16]))
}

// LoadIdentity reads dir/identity.key, creating it (mode 0600) on first
// run. A damaged key file is an error, never silently replaced: the
// device's ID, and every peer's trust in it, depend on it.
func LoadIdentity(dir string) (*Identity, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("crypto: key dir: %w", err)
	}
	path := filepath.Join(dir, KeyFile)
	seed, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		seed = make([]byte, ed25519.SeedSize)
		if _, err := rand.Read(seed); err != nil {
			return nil, err
		}
		if err := writeFileAtomic(path, seed, 0o600); err != nil {
			return nil, fmt.Errorf("crypto: save key: %w", err)
		}
	case err != nil:
		return nil, fmt.Errorf("crypto: read key: %w", err)
	case len(seed) != ed25519.SeedSize:
		return nil, fmt.Errorf("crypto: %s is %d bytes, want %d (damaged?)", path, len(seed), ed25519.SeedSize)
	}
	return NewIdentity(seed), nil
}

// NewIdentity builds an Identity from a seed (tests use fixed seeds).
func NewIdentity(seed []byte) *Identity {
	priv := ed25519.NewKeyFromSeed(seed)
	pub := priv.Public().(ed25519.PublicKey)
	return &Identity{priv: priv, pub: pub, id: IDFor(pub)}
}

// ID returns the device ID.
func (i *Identity) ID() contract.DeviceID { return i.id }

// PublicKey returns the Ed25519 public key.
func (i *Identity) PublicKey() ed25519.PublicKey { return i.pub }

// writeFileAtomic writes to a temp file in the same directory and renames
// it over path, so a crash never leaves a half-written file.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name) // no-op after a successful rename
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(name, perm); err != nil {
		return err
	}
	return os.Rename(name, path)
}

// Package update is DropLink's self-update: a signed manifest
// (latest.json) next to each GitHub release names the newest version and
// its installer, with the installer's SHA-256 signed by the project's
// Ed25519 release key. The app only runs an installer whose hash matches
// and whose signature checks out against the public key compiled in.
package update

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Manifest is latest.json.
type Manifest struct {
	Version   string           `json:"version"`
	Notes     string           `json:"notes"`
	PubDate   string           `json:"pub_date"`
	Platforms map[string]Asset `json:"platforms"`
}

// Asset is one platform's installer.
type Asset struct {
	URL       string `json:"url"`
	Size      int64  `json:"size"`
	SHA256    string `json:"sha256"`    // hex
	Signature string `json:"signature"` // base64 Ed25519 over Message(version, sha256)
}

// Platform names the running OS and architecture, e.g. "windows-x86_64".
func Platform(goos, goarch string) string {
	arch := map[string]string{"amd64": "x86_64", "arm64": "aarch64"}[goarch]
	if arch == "" {
		arch = goarch
	}
	return goos + "-" + arch
}

// Message is what the release key signs: a fixed label, the version and
// the installer's hash, so a signature cannot be replayed for another
// version or file.
func Message(version, sha256hex string) []byte {
	return []byte("droplink-update-v1\n" + version + "\n" + strings.ToLower(sha256hex))
}

// Sign signs an asset for version with the release key.
func Sign(priv ed25519.PrivateKey, version, sha256hex string) string {
	return base64.StdEncoding.EncodeToString(ed25519.Sign(priv, Message(version, sha256hex)))
}

// ErrBadSignature means the manifest was not signed by the release key.
var ErrBadSignature = errors.New("update: signature does not match the release key")

// Verify checks an asset's signature with the base64 public key.
func Verify(pubB64, version string, a Asset) error {
	pub, err := base64.StdEncoding.DecodeString(pubB64)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return errors.New("update: bad public key")
	}
	if _, err := hex.DecodeString(a.SHA256); err != nil || len(a.SHA256) != 64 {
		return fmt.Errorf("update: bad sha256 %q", a.SHA256)
	}
	sig, err := base64.StdEncoding.DecodeString(a.Signature)
	if err != nil || len(sig) != ed25519.SignatureSize {
		return ErrBadSignature
	}
	if !ed25519.Verify(pub, Message(version, a.SHA256), sig) {
		return ErrBadSignature
	}
	return nil
}

// Newer reports whether version a is newer than b (x.y.z, numeric parts;
// a pre-release suffix like "-dev" counts as older than the release).
func Newer(a, b string) bool {
	pa, sa := parse(a)
	pb, sb := parse(b)
	for i := 0; i < 3; i++ {
		if pa[i] != pb[i] {
			return pa[i] > pb[i]
		}
	}
	return sa == "" && sb != "" // 1.2.3 is newer than 1.2.3-dev
}

func parse(v string) ([3]int, string) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	core, suffix, _ := strings.Cut(v, "-")
	var out [3]int
	for i, p := range strings.SplitN(core, ".", 3) {
		n, err := strconv.Atoi(p)
		if err != nil {
			break
		}
		out[i] = n
	}
	return out, suffix
}

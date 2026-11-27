package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

// DefaultURL is the latest release's manifest on GitHub.
const DefaultURL = "https://github.com/vinayakkam/DropLink/releases/latest/download/latest.json"

// InstallerFile is the verified installer's name in Client.Dir. The
// desktop shell runs only this file (install_update), never a path the UI
// passes in.
const InstallerFile = "droplink-update-setup.exe"

const (
	maxManifest  = 64 << 10
	maxInstaller = 512 << 20
)

// Client checks for and downloads updates.
type Client struct {
	URL       string // manifest URL (DefaultURL)
	Current   string // running version
	PublicKey string // base64 Ed25519 (PublicKey)
	Dir       string // where the verified installer is kept
	Platform  string // Platform(runtime.GOOS, runtime.GOARCH)
	HTTP      *http.Client
}

// New returns a Client for the running build.
func New(current, dir string) *Client {
	u := os.Getenv("DROPLINK_UPDATE_URL")
	if u == "" {
		u = DefaultURL
	}
	return &Client{URL: u, Current: current, PublicKey: PublicKey, Dir: dir,
		Platform: Platform(runtime.GOOS, runtime.GOARCH), HTTP: &http.Client{Timeout: 10 * time.Minute}}
}

// Check is the result of a check.
type Check struct {
	Current   string `json:"current"`
	Latest    string `json:"latest"`
	Available bool   `json:"available"`
	Notes     string `json:"notes,omitempty"`
	Size      int64  `json:"size,omitempty"`
	asset     Asset
}

// ErrNoAsset means the release has no installer for this platform.
var ErrNoAsset = errors.New("update: no installer for this platform in the latest release")

// Check fetches the manifest and reports whether a newer, correctly
// signed version exists.
func (c *Client) Check(ctx context.Context) (Check, error) {
	out := Check{Current: c.Current}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.URL, nil)
	if err != nil {
		return out, err
	}
	res, err := c.HTTP.Do(req)
	if err != nil {
		return out, fmt.Errorf("update: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusNotFound {
		return out, nil // no release published yet
	}
	if res.StatusCode != http.StatusOK {
		return out, fmt.Errorf("update: manifest: HTTP %d", res.StatusCode)
	}
	var m Manifest
	if err := json.NewDecoder(io.LimitReader(res.Body, maxManifest)).Decode(&m); err != nil {
		return out, fmt.Errorf("update: manifest: %w", err)
	}
	out.Latest, out.Notes = m.Version, m.Notes
	if !Newer(m.Version, c.Current) {
		return out, nil
	}
	a, ok := m.Platforms[c.Platform]
	if !ok {
		return out, ErrNoAsset
	}
	if err := Verify(c.PublicKey, m.Version, a); err != nil {
		return out, err // never offer something we would refuse to run
	}
	out.Available, out.Size, out.asset = true, a.Size, a
	return out, nil
}

// Download fetches the checked installer, streaming it to disk while
// hashing, and keeps it only if size, SHA-256 and signature all match.
// It returns the installer's path.
func (c *Client) Download(ctx context.Context, chk Check, progress func(done, total int64)) (string, error) {
	if !chk.Available {
		return "", errors.New("update: nothing to download")
	}
	a := chk.asset
	u, err := url.Parse(a.URL)
	if err != nil || u.Scheme != "https" && !isLoopback(u) {
		return "", fmt.Errorf("update: refusing installer URL %q", a.URL)
	}
	if a.Size <= 0 || a.Size > maxInstaller {
		return "", fmt.Errorf("update: installer size %d out of range", a.Size)
	}
	if err := os.MkdirAll(c.Dir, 0o700); err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.URL, nil)
	if err != nil {
		return "", err
	}
	res, err := c.HTTP.Do(req)
	if err != nil {
		return "", fmt.Errorf("update: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("update: installer: HTTP %d", res.StatusCode)
	}

	part := filepath.Join(c.Dir, InstallerFile+".part")
	f, err := os.OpenFile(part, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	pw := &progressWriter{total: a.Size, fn: progress}
	n, err := io.Copy(io.MultiWriter(f, h, pw), io.LimitReader(res.Body, a.Size+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil && n != a.Size {
		err = fmt.Errorf("update: got %d bytes, want %d", n, a.Size)
	}
	if err == nil && hex.EncodeToString(h.Sum(nil)) != a.SHA256 {
		err = errors.New("update: installer hash does not match the signed manifest")
	}
	if err == nil {
		err = Verify(c.PublicKey, chk.Latest, a) // the hash we checked is the one signed
	}
	if err != nil {
		os.Remove(part)
		return "", err
	}
	final := filepath.Join(c.Dir, InstallerFile)
	if err := os.Rename(part, final); err != nil {
		return "", err
	}
	return final, nil
}

func isLoopback(u *url.URL) bool {
	h := u.Hostname()
	return u.Scheme == "http" && (h == "127.0.0.1" || h == "localhost" || h == "::1") // tests only
}

type progressWriter struct {
	done, total int64
	last        time.Time
	fn          func(done, total int64)
}

func (p *progressWriter) Write(b []byte) (int, error) {
	p.done += int64(len(b))
	if p.fn != nil && (time.Since(p.last) > 200*time.Millisecond || p.done >= p.total) {
		p.last = time.Now()
		p.fn(p.done, p.total)
	}
	return len(b), nil
}

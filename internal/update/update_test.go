package update

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

type key struct {
	pub  string
	priv ed25519.PrivateKey
}

func newKey(t *testing.T) key {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return key{base64.StdEncoding.EncodeToString(pub), priv}
}

func digest(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func TestSignAndVerify(t *testing.T) {
	k := newKey(t)
	sum := digest([]byte("installer"))
	a := Asset{SHA256: sum, Signature: Sign(k.priv, "1.2.0", sum)}
	if err := Verify(k.pub, "1.2.0", a); err != nil {
		t.Fatal(err)
	}
	for name, bad := range map[string]func() (string, string, Asset){
		"other version": func() (string, string, Asset) { return k.pub, "1.2.1", a },
		"other file": func() (string, string, Asset) {
			b := a
			b.SHA256 = digest([]byte("evil"))
			return k.pub, "1.2.0", b
		},
		"other key":   func() (string, string, Asset) { return newKey(t).pub, "1.2.0", a },
		"garbage sig": func() (string, string, Asset) { b := a; b.Signature = "!!"; return k.pub, "1.2.0", b },
		"bad pubkey":  func() (string, string, Asset) { return "short", "1.2.0", a },
		"bad sha256":  func() (string, string, Asset) { b := a; b.SHA256 = "xyz"; return k.pub, "1.2.0", b },
	} {
		pub, v, as := bad()
		if err := Verify(pub, v, as); err == nil {
			t.Errorf("%s: verified", name)
		}
	}
}

func TestNewer(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"1.0.1", "1.0.0", true}, {"1.0.0", "1.0.1", false}, {"2.0.0", "1.9.9", true},
		{"1.10.0", "1.9.0", true}, {"0.1.0", "0.1.0-dev", true}, {"0.1.0-dev", "0.1.0", false},
		{"v1.2.3", "1.2.3", false}, {"1.2.3", "1.2.3", false},
	} {
		if got := Newer(c.a, c.b); got != c.want {
			t.Errorf("Newer(%q, %q) = %v", c.a, c.b, got)
		}
	}
	if Platform("windows", "amd64") != "windows-x86_64" || Platform("linux", "arm64") != "linux-aarch64" {
		t.Fatal("platform names")
	}
}

// release serves a manifest and an installer like GitHub would.
type release struct {
	srv       *httptest.Server
	manifest  Manifest
	installer []byte
	served    []byte // what the download actually returns
}

func newRelease(t *testing.T, k key, version string, installer []byte) *release {
	t.Helper()
	r := &release{installer: installer, served: installer}
	mux := http.NewServeMux()
	r.srv = httptest.NewServer(mux)
	t.Cleanup(r.srv.Close)
	sum := digest(installer)
	r.manifest = Manifest{Version: version, Notes: "fixes", Platforms: map[string]Asset{
		"windows-x86_64": {URL: r.srv.URL + "/setup.exe", Size: int64(len(installer)), SHA256: sum, Signature: Sign(k.priv, version, sum)},
	}}
	mux.HandleFunc("/latest.json", func(w http.ResponseWriter, _ *http.Request) { _ = json.NewEncoder(w).Encode(r.manifest) })
	mux.HandleFunc("/setup.exe", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(r.served) })
	return r
}

func client(t *testing.T, k key, r *release, current string) *Client {
	c := New(current, t.TempDir())
	c.URL, c.PublicKey, c.Platform = r.srv.URL+"/latest.json", k.pub, "windows-x86_64"
	return c
}

func TestCheck(t *testing.T) {
	k := newKey(t)
	r := newRelease(t, k, "0.2.0", []byte("setup"))
	ctx := context.Background()

	chk, err := client(t, k, r, "0.1.0").Check(ctx)
	if err != nil || !chk.Available || chk.Latest != "0.2.0" || chk.Notes != "fixes" || chk.Size != 5 {
		t.Fatalf("%+v %v", chk, err)
	}
	if chk, _ := client(t, k, r, "0.2.0").Check(ctx); chk.Available {
		t.Fatal("same version offered")
	}
	if chk, _ := client(t, k, r, "0.3.0").Check(ctx); chk.Available {
		t.Fatal("older version offered")
	}
	c := client(t, k, r, "0.1.0")
	c.Platform = "linux-x86_64"
	if _, err := c.Check(ctx); !errors.Is(err, ErrNoAsset) {
		t.Fatalf("other platform: %v", err)
	}
	if _, err := client(t, newKey(t), r, "0.1.0").Check(ctx); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("foreign key: %v", err)
	}
	c = client(t, k, r, "0.1.0")
	c.URL = r.srv.URL + "/missing.json"
	if chk, err := c.Check(ctx); err != nil || chk.Available {
		t.Fatalf("no release yet: %+v %v", chk, err)
	}
}

func TestDownload(t *testing.T) {
	k := newKey(t)
	installer := bytes.Repeat([]byte("DL"), 50_000)
	r := newRelease(t, k, "0.2.0", installer)
	ctx := context.Background()
	c := client(t, k, r, "0.1.0")
	chk, _ := c.Check(ctx)

	var last int64
	path, err := c.Download(ctx, chk, func(done, total int64) { last = done })
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); !bytes.Equal(got, installer) || filepath.Base(path) != InstallerFile || last != int64(len(installer)) {
		t.Fatalf("path %s, progress %d", path, last)
	}

	// The server swaps the file: same size, different bytes.
	r.served = bytes.Repeat([]byte("XX"), 50_000)
	c2 := client(t, k, r, "0.1.0")
	chk2, _ := c2.Check(ctx)
	if _, err := c2.Download(ctx, chk2, nil); err == nil {
		t.Fatal("tampered installer kept")
	}
	if _, err := os.Stat(filepath.Join(c2.Dir, InstallerFile)); !os.IsNotExist(err) {
		t.Fatal("tampered installer left on disk")
	}
	if entries, _ := os.ReadDir(c2.Dir); len(entries) != 0 {
		t.Fatalf("leftovers: %v", entries)
	}

	// Truncated download.
	r.served = installer[:100]
	if _, err := c2.Download(ctx, chk2, nil); err == nil {
		t.Fatal("short installer kept")
	}
	// Nothing to download.
	if _, err := c2.Download(ctx, Check{}, nil); err == nil {
		t.Fatal("downloaded without an available update")
	}
	// Insecure, non-local URLs are refused.
	chk3 := chk
	chk3.asset.URL = "http://example.org/setup.exe"
	if _, err := c.Download(ctx, chk3, nil); err == nil {
		t.Fatal("plain http installer URL accepted")
	}
}

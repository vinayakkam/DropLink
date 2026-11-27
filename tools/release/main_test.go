package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/vinayakkam/droplink/internal/update"
)

func TestManifestVerifies(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	dir := t.TempDir()
	inst := filepath.Join(dir, InstallerName("1.4.0"))
	_ = os.WriteFile(inst, []byte("installer bytes"), 0o644)
	m, err := MakeManifest(priv, "1.4.0", "notes", inst, time.Unix(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	a := m.Platforms["windows-x86_64"]
	if a.Size != 15 || !strings.HasSuffix(a.URL, "/releases/download/v1.4.0/droplink-1.4.0-windows-x64-setup.exe") || m.PubDate != "1970-01-01T00:00:00Z" {
		t.Fatalf("manifest %+v", m)
	}
	if err := update.Verify(base64.StdEncoding.EncodeToString(pub), "1.4.0", a); err != nil {
		t.Fatal(err)
	}
}

func TestKeygenAndLoadKey(t *testing.T) {
	out := filepath.Join(t.TempDir(), "k", "release.key")
	if err := keygen([]string{"-out", out}); err != nil {
		t.Fatal(err)
	}
	if err := keygen([]string{"-out", out}); err == nil {
		t.Fatal("overwrote an existing release key")
	}
	if _, err := LoadKey(out); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(out, []byte("damaged"), 0o600)
	if _, err := LoadKey(out); err == nil {
		t.Fatal("damaged key loaded")
	}
}

func TestShippedPublicKeyIsValid(t *testing.T) {
	b, err := base64.StdEncoding.DecodeString(update.PublicKey)
	if err != nil || len(b) != ed25519.PublicKeySize {
		t.Fatalf("internal/update.PublicKey is not an Ed25519 key: %v", err)
	}
}

func TestVersionComesFromTauriConfig(t *testing.T) {
	v, err := Version(filepath.Join("..", "..", "desktop", "src-tauri", "tauri.conf.json"))
	if err != nil || !regexp.MustCompile(`^\d+\.\d+\.\d+$`).MatchString(v) {
		t.Fatalf("version %q, %v", v, err)
	}
}

// The release workflow builds both platforms, signs with the secret key
// and never writes the key anywhere it could be uploaded.
func TestReleaseWorkflow(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "release.yml"))
	if err != nil {
		t.Fatal(err)
	}
	wf := string(b)
	for _, want := range []string{
		"tags: [\"v*\"]",
		"secrets.DROPLINK_UPDATE_KEY",
		"go run ./tools/release build",
		"go run ./tools/release sign",
		"--bundles deb,appimage",
		"gh release upload",
		`rm -f "$key"`,
	} {
		if !strings.Contains(wf, want) {
			t.Errorf("release.yml lost %q", want)
		}
	}
	if strings.Contains(wf, "pull_request") {
		t.Error("release.yml must not run on pull requests: they could read the signing key")
	}
	if !strings.Contains(wf, `key="$RUNNER_TEMP/`) {
		t.Error("the signing key must be written under RUNNER_TEMP, outside dist/")
	}
}

// The installer script keeps the promises the README makes.
func TestInstallerScript(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "packaging", "windows", "droplink.iss"))
	if err != nil {
		t.Fatal(err)
	}
	iss := string(b)
	for _, want := range []string{
		"PrivilegesRequired=lowest",
		"WizardStyle=modern dynamic polar",
		"DisableWelcomePage=no",
		"WizardBackImageFileDynamicDark=",
		"CloseApplications=yes",
		`Subkey: "Software\Classes\droplink"`,
		"{param:RELAUNCH|0}",
		"AppId={{DC0033FF-5018-45BA-A0DF-D3422C656703}",
	} {
		if !strings.Contains(iss, want) {
			t.Errorf("droplink.iss lost %q", want)
		}
	}
	// Every art file the script names exists.
	for _, m := range regexp.MustCompile(`\{#Art\}([\w@.-]+\.png)`).FindAllStringSubmatch(iss, -1) {
		if _, err := os.Stat(filepath.Join("..", "..", "packaging", "windows", "art", m[1])); err != nil {
			t.Errorf("missing art %s", m[1])
		}
	}
}

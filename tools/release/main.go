// Command release builds, signs and publishes DropLink releases. No CI:
// run it on the release machine.
//
//	go run ./tools/release keygen            # once: create the update signing key
//	go run ./tools/release build             # app + Windows installer into dist/
//	go run ./tools/release sign -notes "..." # dist/latest.json, signed
//	go run ./tools/release publish           # GitHub release v<version>, asks first
//
// The private key lives outside the repo (DROPLINK_UPDATE_KEY, default
// ~/.droplink/update-signing.key) and must never be committed. Losing it
// means installed apps can no longer be updated, so back it up.
package main

import (
	"bufio"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/vinayakkam/droplink/internal/update"
)

const repo = "vinayakkam/DropLink"

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	var err error
	switch os.Args[1] {
	case "keygen":
		err = keygen(os.Args[2:])
	case "build":
		err = build()
	case "sign":
		err = sign(os.Args[2:])
	case "publish":
		err = publish(os.Args[2:])
	default:
		usage()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "release:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: release keygen | build | sign [-notes text] | publish [-yes]")
	os.Exit(2)
}

// KeyPath is where the private release key lives.
func KeyPath() string {
	if p := os.Getenv("DROPLINK_UPDATE_KEY"); p != "" {
		return p
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".droplink", "update-signing.key")
}

func keygen(args []string) error {
	fs := flag.NewFlagSet("keygen", flag.ContinueOnError)
	out := fs.String("out", KeyPath(), "where to write the private key")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if _, err := os.Stat(*out); err == nil {
		return fmt.Errorf("%s exists; refusing to overwrite a release key", *out)
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(*out), 0o700); err != nil {
		return err
	}
	seed := base64.StdEncoding.EncodeToString(priv.Seed())
	if err := os.WriteFile(*out, []byte(seed+"\n"), 0o600); err != nil {
		return err
	}
	fmt.Println("private key:", *out, "(back it up; never commit it)")
	fmt.Println("public key: ", base64.StdEncoding.EncodeToString(pub))
	fmt.Println("put the public key in internal/update/key.go")
	return nil
}

// LoadKey reads the private key.
func LoadKey(path string) (ed25519.PrivateKey, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("release key: %w (run: go run ./tools/release keygen)", err)
	}
	seed, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(b)))
	if err != nil || len(seed) != ed25519.SeedSize {
		return nil, errors.New("release key: damaged key file")
	}
	return ed25519.NewKeyFromSeed(seed), nil
}

// Version is the app version from tauri.conf.json, the one source of truth.
func Version(confPath string) (string, error) {
	b, err := os.ReadFile(confPath)
	if err != nil {
		return "", err
	}
	var c struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(b, &c); err != nil || c.Version == "" {
		return "", fmt.Errorf("no version in %s", confPath)
	}
	return c.Version, nil
}

func run(dir string, env []string, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	cmd.Env = append(os.Environ(), env...)
	fmt.Println(">", name, strings.Join(args, " "))
	return cmd.Run()
}

func iscc() (string, error) {
	if p := os.Getenv("ISCC"); p != "" {
		return p, nil
	}
	local := os.Getenv("LOCALAPPDATA")
	for _, p := range []string{
		filepath.Join(local, "Programs", "Inno Setup 6", "ISCC.exe"),
		`C:\Program Files (x86)\Inno Setup 6\ISCC.exe`,
		`C:\Program Files\Inno Setup 6\ISCC.exe`,
	} {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	if p, err := exec.LookPath("ISCC"); err == nil {
		return p, nil
	}
	return "", errors.New("inno setup 6.6+ not found (winget install JRSoftware.InnoSetup, or set ISCC)")
}

// InstallerName is the Windows installer's file name for a version.
func InstallerName(version string) string {
	return "droplink-" + version + "-windows-x64-setup.exe"
}

func build() error {
	version, err := Version(filepath.Join("desktop", "src-tauri", "tauri.conf.json"))
	if err != nil {
		return err
	}
	fmt.Println("building DropLink", version)
	env := []string{"DROPLINK_VERSION=" + version}
	if err := run(".", env, "go", "run", "./tools/buildsidecar"); err != nil {
		return err
	}
	// The release app; Windows packaging is Inno Setup, so no Tauri bundle.
	tauriArgs := []string{"run", "tauri", "build"}
	if runtime.GOOS == "windows" {
		tauriArgs = append(tauriArgs, "--", "--no-bundle")
	}
	if err := run("desktop", env, "npm", tauriArgs...); err != nil {
		return err
	}
	if runtime.GOOS != "windows" {
		fmt.Println("bundles are in desktop/src-tauri/target/release/bundle")
		return nil
	}

	stage := filepath.Join("dist", "stage")
	if err := os.RemoveAll(stage); err != nil {
		return err
	}
	if err := os.MkdirAll(stage, 0o755); err != nil {
		return err
	}
	rel := filepath.Join("desktop", "src-tauri", "target", "release")
	for _, f := range []string{"droplink.exe", "droplink-sidecar.exe"} {
		if err := copyFile(filepath.Join(rel, f), filepath.Join(stage, f)); err != nil {
			return err
		}
	}
	compiler, err := iscc()
	if err != nil {
		return err
	}
	absStage, _ := filepath.Abs(stage)
	absOut, _ := filepath.Abs("dist")
	if err := run(".", nil, compiler, "/Q",
		"/DAppVersion="+version, "/DFileVersion="+version+".0",
		"/DStageDir="+absStage, "/DOutputDir="+absOut,
		filepath.Join("packaging", "windows", "droplink.iss")); err != nil {
		return err
	}
	fmt.Println("installer:", filepath.Join("dist", InstallerName(version)))
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// FileSHA256 hashes a file without loading it into memory.
func FileSHA256(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

// MakeManifest signs the installer and builds latest.json.
func MakeManifest(priv ed25519.PrivateKey, version, notes, installer string, now time.Time) (update.Manifest, error) {
	sum, size, err := FileSHA256(installer)
	if err != nil {
		return update.Manifest{}, err
	}
	name := filepath.Base(installer)
	return update.Manifest{
		Version: version,
		Notes:   notes,
		PubDate: now.UTC().Format(time.RFC3339),
		Platforms: map[string]update.Asset{
			"windows-x86_64": {
				URL:       "https://github.com/" + repo + "/releases/download/v" + version + "/" + name,
				Size:      size,
				SHA256:    sum,
				Signature: update.Sign(priv, version, sum),
			},
		},
	}, nil
}

func sign(args []string) error {
	fs := flag.NewFlagSet("sign", flag.ContinueOnError)
	notes := fs.String("notes", "", "release notes shown in the update dialog")
	if err := fs.Parse(args); err != nil {
		return err
	}
	version, err := Version(filepath.Join("desktop", "src-tauri", "tauri.conf.json"))
	if err != nil {
		return err
	}
	priv, err := LoadKey(KeyPath())
	if err != nil {
		return err
	}
	m, err := MakeManifest(priv, version, *notes, filepath.Join("dist", InstallerName(version)), time.Now())
	if err != nil {
		return err
	}
	// Self-check against the public key the app ships with.
	if err := update.Verify(update.PublicKey, version, m.Platforms["windows-x86_64"]); err != nil {
		return fmt.Errorf("signed with a key the app does not trust (internal/update/key.go): %w", err)
	}
	b, _ := json.MarshalIndent(m, "", "  ")
	out := filepath.Join("dist", "latest.json")
	if err := os.WriteFile(out, b, 0o644); err != nil {
		return err
	}
	fmt.Println("wrote", out)
	return nil
}

func publish(args []string) error {
	fs := flag.NewFlagSet("publish", flag.ContinueOnError)
	yes := fs.Bool("yes", false, "do not ask for confirmation")
	if err := fs.Parse(args); err != nil {
		return err
	}
	version, err := Version(filepath.Join("desktop", "src-tauri", "tauri.conf.json"))
	if err != nil {
		return err
	}
	installer := filepath.Join("dist", InstallerName(version))
	manifest := filepath.Join("dist", "latest.json")
	b, err := os.ReadFile(manifest)
	if err != nil {
		return fmt.Errorf("%w (run sign first)", err)
	}
	var m update.Manifest
	if err := json.Unmarshal(b, &m); err != nil || m.Version != version {
		return fmt.Errorf("%s is for version %q, not %s; run sign again", manifest, m.Version, version)
	}
	if !*yes {
		fmt.Printf("Publish DropLink v%s to github.com/%s/releases (public)? [y/N] ", version, repo)
		answer, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		if strings.ToLower(strings.TrimSpace(answer)) != "y" {
			return errors.New("not published")
		}
	}
	notes := m.Notes
	if notes == "" {
		notes = "DropLink " + version
	}
	return run(".", nil, "gh", "release", "create", "v"+version, installer, manifest,
		"--repo", repo, "--title", "DropLink "+version, "--notes", notes)
}

// Command buildsidecar builds cmd/droplink-sidecar into the place Tauri's
// externalBin expects: desktop/src-tauri/binaries/droplink-sidecar-<triple>.
// It is a Go program instead of a shell line so it runs the same on
// Windows, macOS and Linux. Run from the repo root: go run ./tools/buildsidecar
package main

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

func main() {
	if err := build(); err != nil {
		fmt.Fprintln(os.Stderr, "buildsidecar:", err)
		os.Exit(1)
	}
}

func build() error {
	triple, err := hostTriple()
	if err != nil {
		return err
	}
	name := "droplink-sidecar-" + triple
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	out := filepath.Join("desktop", "src-tauri", "binaries", name)

	ldflags := "-s -w"
	if v := os.Getenv("DROPLINK_VERSION"); v != "" {
		ldflags += " -X main.version=" + v
	}
	cmd := exec.Command("go", "build", "-trimpath", "-ldflags", ldflags, "-o", out, "./cmd/droplink-sidecar")
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if err := cmd.Run(); err != nil {
		return err
	}
	fmt.Println("built", out)
	return nil
}

// hostTriple asks rustc, because Tauri names sidecars by Rust target triple.
func hostTriple() (string, error) {
	var buf bytes.Buffer
	cmd := exec.Command("rustc", "-vV")
	cmd.Stdout = &buf
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("rustc -vV: %w (is Rust installed and on PATH?)", err)
	}
	sc := bufio.NewScanner(&buf)
	for sc.Scan() {
		if t, ok := strings.CutPrefix(sc.Text(), "host: "); ok {
			return strings.TrimSpace(t), nil
		}
	}
	return "", fmt.Errorf("rustc -vV printed no host line")
}

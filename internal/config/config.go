// Package config holds the sidecar's settings: defaults, then environment
// variables, then command-line flags, in that order of precedence.
package config

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Origins the Tauri webview loads the UI from. WebView2 uses
// http(s)://tauri.localhost, WebKit uses tauri://localhost.
var TauriOrigins = []string{
	"tauri://localhost",
	"http://tauri.localhost",
	"https://tauri.localhost",
}

// Config is everything the sidecar needs to start.
type Config struct {
	// Addr is the API listen address. It must be a loopback address; port 0
	// picks a free port, which the sidecar reports on its ready line.
	Addr string
	// DataDir holds device keys, trusted devices and history. Never file data.
	DataDir string
	// DeviceName is what other peers see on their radar.
	DeviceName string
	// MemLimitMiB is the Go soft memory limit (GOMEMLIMIT wins when set).
	MemLimitMiB int
	// ChunkKiB is the transfer chunk size, and so the size of each pooled buffer.
	ChunkKiB int
	// WatchStdin makes the sidecar exit when stdin closes, so it never
	// outlives the desktop app that started it.
	WatchStdin bool
	// WebDir, when set, serves the UI from disk for browser development.
	WebDir string
	// Origins allowed to call the API from a browser context (CORS).
	Origins []string
}

// Default returns the settings used when nothing is overridden.
func Default() Config {
	name, err := os.Hostname()
	if err != nil || name == "" {
		name = "droplink-node"
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = os.TempDir()
	}
	return Config{
		Addr:        "127.0.0.1:0",
		DataDir:     filepath.Join(dir, "DropLink"),
		DeviceName:  name,
		MemLimitMiB: 64,
		ChunkKiB:    64,
		Origins:     append([]string(nil), TauriOrigins...),
	}
}

// Parse builds a Config from defaults, getenv and args. getenv is passed in
// so tests never touch the real environment.
func Parse(args []string, getenv func(string) string) (Config, error) {
	c := Default()
	if err := c.applyEnv(getenv); err != nil {
		return Config{}, err
	}

	fs := flag.NewFlagSet("droplink-sidecar", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&c.Addr, "addr", c.Addr, "loopback API address (port 0 = any free port)")
	fs.StringVar(&c.DataDir, "data-dir", c.DataDir, "directory for keys, trust list and history")
	fs.StringVar(&c.DeviceName, "name", c.DeviceName, "device name shown to peers")
	fs.IntVar(&c.MemLimitMiB, "mem-limit", c.MemLimitMiB, "soft memory limit in MiB")
	fs.IntVar(&c.ChunkKiB, "chunk", c.ChunkKiB, "transfer chunk size in KiB")
	fs.BoolVar(&c.WatchStdin, "watch-stdin", c.WatchStdin, "exit when stdin closes")
	fs.StringVar(&c.WebDir, "web-dir", c.WebDir, "serve the UI from this directory (development)")
	fs.Func("allow-origin", "extra CORS origin (repeatable)", func(v string) error {
		c.Origins = append(c.Origins, strings.TrimRight(v, "/"))
		return nil
	})
	if err := fs.Parse(args); err != nil {
		return Config{}, err
	}
	if fs.NArg() > 0 {
		return Config{}, fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	return c, c.Validate()
}

func (c *Config) applyEnv(getenv func(string) string) error {
	if v := getenv("DROPLINK_ADDR"); v != "" {
		c.Addr = v
	}
	if v := getenv("DROPLINK_DATA_DIR"); v != "" {
		c.DataDir = v
	}
	if v := getenv("DROPLINK_NAME"); v != "" {
		c.DeviceName = v
	}
	for key, dst := range map[string]*int{
		"DROPLINK_MEMLIMIT_MIB": &c.MemLimitMiB,
		"DROPLINK_CHUNK_KIB":    &c.ChunkKiB,
	} {
		if v := getenv(key); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil {
				return fmt.Errorf("%s: %q is not a number", key, v)
			}
			*dst = n
		}
	}
	return nil
}

// Validate rejects settings that would be unsafe or wasteful.
func (c Config) Validate() error {
	host, _, err := net.SplitHostPort(c.Addr)
	if err != nil {
		return fmt.Errorf("addr %q: %w", c.Addr, err)
	}
	if !IsLoopback(host) {
		// The API has no login beyond the per-run token; never expose it.
		return fmt.Errorf("addr %q: the API must listen on loopback only", c.Addr)
	}
	switch {
	case strings.TrimSpace(c.DeviceName) == "":
		return errors.New("device name is empty")
	case len(c.DeviceName) > 64:
		return errors.New("device name is longer than 64 bytes")
	case c.MemLimitMiB < 16 || c.MemLimitMiB > 4096:
		return fmt.Errorf("mem-limit %d MiB is outside 16..4096", c.MemLimitMiB)
	case c.ChunkKiB < 4 || c.ChunkKiB > 1024:
		return fmt.Errorf("chunk %d KiB is outside 4..1024", c.ChunkKiB)
	case c.DataDir == "":
		return errors.New("data dir is empty")
	}
	return nil
}

// IsLoopback reports whether host is localhost or a loopback IP.
func IsLoopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// ChunkBytes is ChunkKiB in bytes.
func (c Config) ChunkBytes() int { return c.ChunkKiB << 10 }

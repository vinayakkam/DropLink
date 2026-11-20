package config

import (
	"slices"
	"strings"
	"testing"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestDefaultIsValid(t *testing.T) {
	c := Default()
	if err := c.Validate(); err != nil {
		t.Fatalf("Default().Validate() = %v", err)
	}
	if c.Addr != "127.0.0.1:0" || c.MemLimitMiB != 64 || c.ChunkKiB != 64 {
		t.Fatalf("unexpected defaults: %+v", c)
	}
	for _, o := range TauriOrigins {
		if !slices.Contains(c.Origins, o) {
			t.Errorf("default origins miss %q", o)
		}
	}
}

func TestPrecedenceFlagsOverEnvOverDefaults(t *testing.T) {
	c, err := Parse([]string{"-name", "from-flag", "-chunk", "128"}, env(map[string]string{
		"DROPLINK_NAME":         "from-env",
		"DROPLINK_DATA_DIR":     "/tmp/dl",
		"DROPLINK_MEMLIMIT_MIB": "96",
		"DROPLINK_CHUNK_KIB":    "32",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if c.DeviceName != "from-flag" {
		t.Errorf("name = %q, want flag value", c.DeviceName)
	}
	if c.ChunkKiB != 128 || c.ChunkBytes() != 128<<10 {
		t.Errorf("chunk = %d, want 128 from flag", c.ChunkKiB)
	}
	if c.MemLimitMiB != 96 || c.DataDir != "/tmp/dl" {
		t.Errorf("env not applied: %+v", c)
	}
}

func TestParseRejects(t *testing.T) {
	cases := []struct {
		name string
		args []string
		env  map[string]string
		want string
	}{
		{"public addr", []string{"-addr", "0.0.0.0:80"}, nil, "loopback"},
		{"lan addr", []string{"-addr", "192.168.1.5:0"}, nil, "loopback"},
		{"no port", []string{"-addr", "127.0.0.1"}, nil, "addr"},
		{"empty name", []string{"-name", "  "}, nil, "name"},
		{"long name", []string{"-name", strings.Repeat("x", 65)}, nil, "64"},
		{"mem too low", []string{"-mem-limit", "8"}, nil, "mem-limit"},
		{"chunk too big", []string{"-chunk", "4096"}, nil, "chunk"},
		{"bad env number", nil, map[string]string{"DROPLINK_CHUNK_KIB": "lots"}, "not a number"},
		{"stray arg", []string{"oops"}, nil, "unexpected"},
		{"unknown flag", []string{"-nope"}, nil, "nope"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse(tc.args, env(tc.env))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}

func TestAllowOriginAppends(t *testing.T) {
	c, err := Parse([]string{"-allow-origin", "http://localhost:5173/"}, env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(c.Origins, "http://localhost:5173") {
		t.Fatalf("origins = %v, want trailing slash trimmed and appended", c.Origins)
	}
}

func TestIsLoopback(t *testing.T) {
	for host, want := range map[string]bool{
		"localhost": true, "LOCALHOST": true, "127.0.0.1": true, "127.3.2.1": true, "::1": true,
		"0.0.0.0": false, "10.0.0.1": false, "evil.example": false, "": false,
	} {
		if got := IsLoopback(host); got != want {
			t.Errorf("IsLoopback(%q) = %v, want %v", host, got, want)
		}
	}
}

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/vinayakkam/droplink/internal/server"
	"github.com/vinayakkam/droplink/internal/testutil"
)

// startSidecar runs the real sidecar loop in-process and returns its ready
// line, the stdin writer (close it to stop) and the exit code channel.
func startSidecar(t *testing.T, ctx context.Context, args ...string) (server.Ready, io.WriteCloser, <-chan int) {
	t.Helper()
	stdinR, stdinW := io.Pipe()
	stdoutR, stdoutW := io.Pipe()
	exit := make(chan int, 1)
	env := map[string]string{"DROPLINK_DATA_DIR": t.TempDir(), "DROPLINK_NAME": "test-node"}
	go func() {
		exit <- run(ctx, args, func(k string) string { return env[k] }, stdinR, stdoutW, io.Discard)
		stdoutW.Close()
	}()

	lines := make(chan string, 1)
	go func() {
		sc := bufio.NewScanner(stdoutR)
		for sc.Scan() {
			select {
			case lines <- sc.Text():
			default:
			}
		}
	}()
	select {
	case line := <-lines:
		var r server.Ready
		js, ok := strings.CutPrefix(line, server.ReadyPrefix)
		if !ok || json.Unmarshal([]byte(js), &r) != nil {
			t.Fatalf("bad ready line %q", line)
		}
		if r.Port == 0 || len(r.Token) != 64 || r.Version == "" {
			t.Fatalf("ready = %+v", r)
		}
		return r, stdinW, exit
	case code := <-exit:
		t.Fatalf("sidecar exited with %d before ready", code)
	case <-time.After(5 * time.Second):
		t.Fatal("no ready line within 5 s")
	}
	panic("unreachable")
}

func get(t *testing.T, r server.Ready, path string, auth bool) int {
	t.Helper()
	req, _ := http.NewRequest("GET", fmt.Sprintf("http://127.0.0.1:%d%s", r.Port, path), nil)
	if auth {
		req.Header.Set("Authorization", "Bearer "+r.Token)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	return res.StatusCode
}

func TestSidecarServesAndStopsOnStdinClose(t *testing.T) {
	testutil.NoLeaks(t)
	defer http.DefaultClient.CloseIdleConnections()

	ready, stdin, exit := startSidecar(t, context.Background(), "-watch-stdin")
	if code := get(t, ready, "/api/health", false); code != 200 {
		t.Fatalf("health = %d", code)
	}
	if code := get(t, ready, "/api/info", false); code != 401 {
		t.Fatalf("info without token = %d", code)
	}
	if code := get(t, ready, "/api/info", true); code != 200 {
		t.Fatalf("info with token = %d", code)
	}

	stdin.Close() // the desktop shell went away
	select {
	case code := <-exit:
		if code != 0 {
			t.Fatalf("exit code %d", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("sidecar did not stop after stdin closed")
	}
}

func TestSidecarStopsOnContextCancel(t *testing.T) {
	testutil.NoLeaks(t)
	ctx, cancel := context.WithCancel(context.Background())
	_, stdin, exit := startSidecar(t, ctx)
	defer stdin.Close()
	cancel() // SIGINT / SIGTERM path
	select {
	case code := <-exit:
		if code != 0 {
			t.Fatalf("exit code %d", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("sidecar ignored cancellation")
	}
}

func TestSidecarRejectsBadConfig(t *testing.T) {
	code := run(context.Background(), []string{"-addr", "0.0.0.0:0"},
		func(string) string { return "" }, strings.NewReader(""), io.Discard, io.Discard)
	if code != 2 {
		t.Fatalf("exit code %d, want 2 for a non-loopback address", code)
	}
}

func TestTokensAreUnique(t *testing.T) {
	a, _ := newToken()
	b, _ := newToken()
	if a == b || len(a) != 64 {
		t.Fatalf("tokens %q %q", a, b)
	}
}

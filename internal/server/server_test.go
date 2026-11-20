package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/vinayakkam/droplink/internal/config"
)

const token = "0123456789abcdef0123456789abcdef"

func newTestServer(t *testing.T, mutate func(*config.Config)) *Server {
	t.Helper()
	cfg := config.Default()
	cfg.DeviceName = "test-node"
	if mutate != nil {
		mutate(&cfg)
	}
	return New(cfg, token, "1.2.3")
}

func do(h http.Handler, method, target string, hdr map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, nil)
	req.Host = "127.0.0.1:5000"
	for k, v := range hdr {
		if k == "Host" {
			req.Host = v
			continue
		}
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestHealthNeedsNoToken(t *testing.T) {
	rec := do(newTestServer(t, nil).Handler(), "GET", "/api/health", nil)
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body["ok"] != true {
		t.Fatalf("body = %s (%v)", rec.Body, err)
	}
}

func TestInfoAuth(t *testing.T) {
	h := newTestServer(t, nil).Handler()
	cases := []struct {
		name   string
		target string
		hdr    map[string]string
		want   int
	}{
		{"no token", "/api/info", nil, 401},
		{"wrong token", "/api/info", map[string]string{"Authorization": "Bearer nope"}, 401},
		{"bearer", "/api/info", map[string]string{"Authorization": "Bearer " + token}, 200},
		{"query (SSE)", "/api/info?token=" + token, nil, 200},
		{"token prefix only", "/api/info?token=" + token[:10], nil, 401},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := do(h, "GET", tc.target, tc.hdr)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d (%s)", rec.Code, tc.want, rec.Body)
			}
			if tc.want == 401 {
				var e map[string]string
				if json.Unmarshal(rec.Body.Bytes(), &e) != nil || e["error"] == "" {
					t.Fatalf("error body = %s, want {\"error\":...}", rec.Body)
				}
			}
		})
	}
}

func TestInfoBody(t *testing.T) {
	s := newTestServer(t, nil)
	s.Handle("GET /api/x", "xfeature", http.NotFoundHandler())
	s.Handle("GET /api/y", "xfeature", http.NotFoundHandler()) // same feature listed once
	rec := do(s.Handler(), "GET", "/api/info", map[string]string{"Authorization": "Bearer " + token})
	var info Info
	if err := json.Unmarshal(rec.Body.Bytes(), &info); err != nil {
		t.Fatal(err)
	}
	if info.Version != "1.2.3" || info.Device != "test-node" || info.ChunkKiB != 64 {
		t.Fatalf("info = %+v", info)
	}
	if !slices.Equal(info.Features, []string{"xfeature"}) {
		t.Fatalf("features = %v", info.Features)
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("API responses must not be cached")
	}
}

func TestHandledRoutesNeedToken(t *testing.T) {
	s := newTestServer(t, nil)
	s.Handle("GET /api/secret", "secret", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		WriteJSON(w, 200, "ok")
	}))
	if rec := do(s.Handler(), "GET", "/api/secret", nil); rec.Code != 401 {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestHostCheckBlocksRebinding(t *testing.T) {
	h := newTestServer(t, nil).Handler()
	for host, want := range map[string]int{
		"127.0.0.1:5000":   200,
		"localhost:5000":   200,
		"[::1]:5000":       200,
		"evil.example":     403,
		"evil.example:80":  403,
		"192.168.1.9:5000": 403,
	} {
		if rec := do(h, "GET", "/api/health", map[string]string{"Host": host}); rec.Code != want {
			t.Errorf("Host %q: status = %d, want %d", host, rec.Code, want)
		}
	}
}

func TestCORS(t *testing.T) {
	h := newTestServer(t, nil).Handler()

	rec := do(h, "OPTIONS", "/api/info", map[string]string{"Origin": "http://tauri.localhost"})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("preflight status = %d, want 204 without a token", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "http://tauri.localhost" {
		t.Fatalf("allow-origin = %q", got)
	}

	rec = do(h, "GET", "/api/health", map[string]string{"Origin": "https://evil.example"})
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("foreign origin got allow-origin %q", got)
	}
}

func TestDevSessionOnlyWithWebDir(t *testing.T) {
	if rec := do(newTestServer(t, nil).Handler(), "GET", "/dev/session", nil); rec.Code != 404 {
		t.Fatalf("without -web-dir: status = %d, want 404", rec.Code)
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<h1>hi</h1>"), 0o644); err != nil {
		t.Fatal(err)
	}
	h := newTestServer(t, func(c *config.Config) { c.WebDir = dir }).Handler()
	rec := do(h, "GET", "/dev/session", nil)
	var body map[string]string
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &body) != nil || body["token"] != token {
		t.Fatalf("dev session = %d %s", rec.Code, rec.Body)
	}
	if rec := do(h, "GET", "/", nil); rec.Code != 200 {
		t.Fatalf("static index: status = %d", rec.Code)
	}
}

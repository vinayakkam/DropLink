// Package server is the sidecar's local HTTP API. The desktop UI is its only
// client: it listens on loopback, checks the Host header against DNS
// rebinding, and wants the per-run token on every call except /api/health.
package server

import (
	"crypto/subtle"
	"encoding/json"
	"net"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/vinayakkam/droplink/internal/config"
)

// ReadyPrefix starts the one line the sidecar prints on stdout once it is
// listening. The desktop shell parses it (desktop/src-tauri/src/sidecar.rs).
const ReadyPrefix = "DROPLINK_READY "

// Ready is the JSON after ReadyPrefix.
type Ready struct {
	Port    int    `json:"port"`
	Token   string `json:"token"`
	Version string `json:"version"`
}

// Info is what GET /api/info returns.
type Info struct {
	Version  string   `json:"version"`
	Device   string   `json:"device"`
	Uptime   int64    `json:"uptimeSec"`
	ChunkKiB int      `json:"chunkKiB"`
	Features []string `json:"features"`
}

// Server holds the API's state. Feature handlers are added by later
// branches through Handle, so this file stays small.
type Server struct {
	cfg      config.Config
	token    []byte
	version  string
	started  time.Time
	mux      *http.ServeMux
	features []string
}

// New builds a Server. token must be the value printed on the ready line.
func New(cfg config.Config, token, version string) *Server {
	s := &Server{
		cfg:     cfg,
		token:   []byte(token),
		version: version,
		started: time.Now(),
		mux:     http.NewServeMux(),
	}
	s.mux.HandleFunc("GET /api/health", s.health)
	s.mux.Handle("GET /api/info", s.auth(http.HandlerFunc(s.info)))
	if cfg.WebDir != "" {
		// Development only: lets a plain browser open the UI and fetch the
		// token from the same origin. Tauri builds never pass -web-dir.
		files := http.FileServer(http.Dir(cfg.WebDir))
		s.mux.Handle("GET /", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-cache") // edits show on reload
			files.ServeHTTP(w, r)
		}))
		s.mux.HandleFunc("GET /dev/session", s.devSession)
	}
	return s
}

// Handle registers an authenticated route and lists feature in /api/info,
// so the UI shows a control only when its backend exists.
func (s *Server) Handle(pattern, feature string, h http.Handler) {
	s.mux.Handle(pattern, s.auth(h))
	if feature != "" && !slices.Contains(s.features, feature) {
		s.features = append(s.features, feature)
	}
}

// Handler returns the full middleware chain.
func (s *Server) Handler() http.Handler {
	return s.hostCheck(s.cors(s.mux))
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	WriteJSON(w, http.StatusOK, map[string]any{"ok": true, "version": s.version})
}

func (s *Server) info(w http.ResponseWriter, _ *http.Request) {
	WriteJSON(w, http.StatusOK, Info{
		Version:  s.version,
		Device:   s.cfg.DeviceName,
		Uptime:   int64(time.Since(s.started).Seconds()),
		ChunkKiB: s.cfg.ChunkKiB,
		Features: append([]string{}, s.features...),
	})
}

func (s *Server) devSession(w http.ResponseWriter, _ *http.Request) {
	WriteJSON(w, http.StatusOK, map[string]string{"token": string(s.token)})
}

// auth accepts "Authorization: Bearer <token>", or ?token= for EventSource,
// which cannot set headers.
func (s *Server) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if got == "" {
			got = r.URL.Query().Get("token")
		}
		if subtle.ConstantTimeCompare([]byte(got), s.token) != 1 {
			WriteError(w, http.StatusUnauthorized, "missing or wrong token")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// hostCheck blocks DNS rebinding: a web page on evil.example resolving to
// 127.0.0.1 still sends Host: evil.example.
func (s *Server) hostCheck(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		host = strings.Trim(host, "[]")
		if !config.IsLoopback(host) {
			WriteError(w, http.StatusForbidden, "host not allowed")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && slices.Contains(s.cfg.Origins, origin) {
			h := w.Header()
			h.Set("Access-Control-Allow-Origin", origin)
			h.Set("Vary", "Origin")
			h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
			h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			h.Set("Access-Control-Max-Age", "600")
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// WriteJSON writes v with status. Handlers in later branches use it too.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// WriteError writes {"error": msg}, the one error shape of the API.
func WriteError(w http.ResponseWriter, status int, msg string) {
	WriteJSON(w, status, map[string]string{"error": msg})
}

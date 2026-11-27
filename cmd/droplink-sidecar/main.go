// Command droplink-sidecar is DropLink's engine: the desktop app starts it,
// reads its ready line, and talks to it over the loopback API.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"
	"time"

	"github.com/vinayakkam/droplink/internal/app"
	"github.com/vinayakkam/droplink/internal/config"
	"github.com/vinayakkam/droplink/internal/server"
)

// version is set with -ldflags "-X main.version=...".
var version = "0.1.0-dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.Getenv, os.Stdin, os.Stdout, os.Stderr))
}

// run is main without process globals, so tests can drive a whole sidecar.
func run(ctx context.Context, args []string, getenv func(string) string,
	stdin io.Reader, stdout, stderr io.Writer) int {
	log := slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	cfg, err := config.Parse(args, getenv)
	if err != nil {
		log.Error("bad config", "err", err)
		return 2
	}
	if getenv("GOMEMLIMIT") == "" {
		debug.SetMemoryLimit(int64(cfg.MemLimitMiB) << 20)
	}

	providers, err := app.NewProviders(cfg)
	if err != nil {
		log.Error("providers", "err", err)
		return 1
	}
	defer providers.Close()

	token, err := newToken()
	if err != nil {
		log.Error("token", "err", err)
		return 1
	}
	ln, err := net.Listen("tcp", cfg.Addr)
	if err != nil {
		log.Error("listen", "addr", cfg.Addr, "err", err)
		return 1
	}

	api := server.New(cfg, token, version)
	mgr, err := wire(cfg, providers, api, log)
	if err != nil {
		log.Error("wire", "err", err)
		return 1
	}
	defer mgr.Close()

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	srv := &http.Server{
		Handler:           api.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    16 << 10,
		// Requests end with the run context, so open event streams never
		// hold up shutdown.
		BaseContext: func(net.Listener) context.Context { return ctx },
	}
	if cfg.WatchStdin {
		go func() {
			// Copy until EOF: the parent closed our stdin or died.
			_, _ = io.Copy(io.Discard, stdin)
			log.Info("stdin closed, shutting down")
			cancel()
		}()
	}

	go func() {
		if err := providers.Discovery.Run(ctx); err != nil {
			log.Warn("discovery stopped", "err", err)
		}
	}()

	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()

	ready, _ := json.Marshal(server.Ready{
		Port:    ln.Addr().(*net.TCPAddr).Port,
		Token:   token,
		Version: version,
	})
	fmt.Fprintf(stdout, "%s%s\n", server.ReadyPrefix, ready)
	log.Info("listening", "addr", ln.Addr().String(), "device", cfg.DeviceName, "id", providers.Self.ID)

	select {
	case err := <-errc:
		if !errors.Is(err, http.ErrServerClosed) {
			log.Error("serve", "err", err)
			return 1
		}
	case <-ctx.Done():
	}

	cancel() // ends event streams
	shutdownCtx, done := context.WithTimeout(context.Background(), 3*time.Second)
	defer done()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Warn("shutdown", "err", err)
	}
	return 0
}

func newToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

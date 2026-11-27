// Command droplink-signal runs the signaling hub: devices meet in a room
// here to set up WebRTC, then talk to each other directly. It keeps rooms
// in memory only and never stores or logs what it relays.
//
//	droplink-signal -addr :47480
//
// Devices point at it with DROPLINK_SIGNAL_URL=ws://<host>:47480/ws
// (wss:// behind a TLS proxy).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	ossignal "os/signal"
	"runtime/debug"
	"syscall"
	"time"

	"github.com/vinayakkam/droplink/internal/server"
	"github.com/vinayakkam/droplink/internal/signal"
)

func main() {
	ctx, stop := ossignal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.Stdout, os.Stderr))
}

// run is main without process globals. It prints the listening address
// on stdout once it accepts connections.
func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("droplink-signal", flag.ContinueOnError)
	fs.SetOutput(stderr)
	addr := fs.String("addr", ":47480", "listen address")
	members := fs.Int("max-members", 8, "devices per room")
	rooms := fs.Int("max-rooms", 10_000, "rooms per hub")
	idle := fs.Duration("idle", 10*time.Minute, "close rooms idle this long")
	memMiB := fs.Int("mem-limit", 32, "soft memory limit in MiB (GOMEMLIMIT wins)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	log := slog.New(slog.NewTextHandler(stderr, nil))
	if os.Getenv("GOMEMLIMIT") == "" {
		debug.SetMemoryLimit(int64(*memMiB) << 20)
	}

	reg := signal.NewRegistry(signal.Limits{MaxMembers: *members, MaxRooms: *rooms, Idle: *idle})
	hub := signal.NewServer(reg)
	mux := http.NewServeMux()
	mux.Handle("GET /ws", hub)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		r, m := reg.Stats() // counts only, never contents
		server.WriteJSON(w, http.StatusOK, map[string]int{"rooms": r, "members": m})
	})

	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Error("listen", "addr", *addr, "err", err)
		return 1
	}
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second, MaxHeaderBytes: 8 << 10}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go hub.Run(ctx)
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	fmt.Fprintf(stdout, "droplink-signal listening on %s\n", ln.Addr())

	select {
	case err := <-errc:
		if !errors.Is(err, http.ErrServerClosed) {
			log.Error("serve", "err", err)
			return 1
		}
	case <-ctx.Done():
	}
	sctx, done := context.WithTimeout(context.Background(), 5*time.Second)
	defer done()
	_ = srv.Shutdown(sctx)
	return 0
}

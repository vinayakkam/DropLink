package api

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/vinayakkam/droplink/internal/manager"
	"github.com/vinayakkam/droplink/internal/server"
	"github.com/vinayakkam/droplink/internal/update"
)

// Updater is the sidecar's side of self-update: check, then download and
// verify; the desktop shell then runs the verified installer.
type Updater struct {
	Client *update.Client
	Events *manager.Manager

	mu      sync.Mutex
	last    update.Check
	running bool
}

// UpdateState is the "update" event's payload.
type UpdateState struct {
	Phase string `json:"phase"` // downloading | ready | failed
	Done  int64  `json:"done"`
	Total int64  `json:"total"`
	Error string `json:"error,omitempty"`
}

func registerUpdate(srv *server.Server, u *Updater) {
	srv.Handle("GET /api/update", "update", http.HandlerFunc(u.check))
	srv.Handle("POST /api/update/download", "update", http.HandlerFunc(u.download))
}

func (u *Updater) check(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	chk, err := u.Client.Check(ctx)
	if err != nil {
		server.WriteError(w, http.StatusBadGateway, err.Error())
		return
	}
	u.mu.Lock()
	u.last = chk
	u.mu.Unlock()
	server.WriteJSON(w, http.StatusOK, chk)
}

// download starts fetching the checked version in the background; the UI
// follows "update" events and asks the shell to install when ready.
func (u *Updater) download(w http.ResponseWriter, _ *http.Request) {
	u.mu.Lock()
	chk, busy := u.last, u.running
	if !busy && chk.Available {
		u.running = true
	}
	u.mu.Unlock()
	switch {
	case busy:
		server.WriteError(w, http.StatusConflict, "a download is already running")
		return
	case !chk.Available:
		server.WriteError(w, http.StatusConflict, "no update to download; check first")
		return
	}
	go func() {
		defer func() {
			u.mu.Lock()
			u.running = false
			u.mu.Unlock()
		}()
		emit := func(s UpdateState) { u.Events.Emit(manager.Event{Type: "update", Update: s}) }
		_, err := u.Client.Download(context.Background(), chk, func(done, total int64) {
			emit(UpdateState{Phase: "downloading", Done: done, Total: total})
		})
		if err != nil {
			emit(UpdateState{Phase: "failed", Error: err.Error()})
			return
		}
		emit(UpdateState{Phase: "ready", Done: chk.Size, Total: chk.Size})
	}()
	w.WriteHeader(http.StatusAccepted)
}

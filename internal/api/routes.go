package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/vinayakkam/droplink/internal/contract"
	"github.com/vinayakkam/droplink/internal/manager"
	"github.com/vinayakkam/droplink/internal/server"
)

// Roomer is implemented by the WebRTC transport.
type Roomer interface {
	JoinRoom(ctx context.Context, code string) (string, error)
	LeaveRoom()
	RoomMembers() []contract.Peer
}

// Deps are what the routes use.
type Deps struct {
	Manager  *manager.Manager
	Crypto   contract.Crypto
	History  contract.History
	Settings *SettingsStore
	Rooms    Roomer // nil: no rooms feature
	// Heartbeat for the event stream (15 s).
	Heartbeat time.Duration
}

// Register adds every route to srv.
func Register(srv *server.Server, d Deps) {
	if d.Heartbeat <= 0 {
		d.Heartbeat = 15 * time.Second
	}
	h := &handlers{d: d}
	srv.Handle("GET /api/events", "events", http.HandlerFunc(h.events))
	srv.Handle("GET /api/peers", "peers", http.HandlerFunc(h.peers))
	srv.Handle("GET /api/transfers", "send", http.HandlerFunc(h.listTransfers))
	srv.Handle("POST /api/transfers", "send", http.HandlerFunc(h.send))
	srv.Handle("POST /api/transfers/{id}/{action}", "control", http.HandlerFunc(h.control))
	srv.Handle("GET /api/trust", "trust", http.HandlerFunc(h.listTrust))
	srv.Handle("PUT /api/trust/{id}", "trust", http.HandlerFunc(h.trust))
	srv.Handle("DELETE /api/trust/{id}", "trust", http.HandlerFunc(h.untrust))
	srv.Handle("GET /api/settings", "settings", http.HandlerFunc(h.getSettings))
	srv.Handle("PUT /api/settings", "settings", http.HandlerFunc(h.putSettings))
	srv.Handle("GET /api/history", "history", http.HandlerFunc(h.listHistory))
	srv.Handle("DELETE /api/history", "history", http.HandlerFunc(h.clearHistory))
	if d.Rooms != nil {
		srv.Handle("GET /api/rooms", "rooms", http.HandlerFunc(h.room))
		srv.Handle("POST /api/rooms", "rooms", http.HandlerFunc(h.createRoom))
		srv.Handle("POST /api/rooms/{code}/join", "rooms", http.HandlerFunc(h.joinRoom))
		srv.Handle("DELETE /api/rooms/current", "rooms", http.HandlerFunc(h.leaveRoom))
		registerQR(srv)
	}
}

type handlers struct {
	d    Deps
	code string // current room code
}

const maxBody = 2 << 20

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		server.WriteError(w, http.StatusBadRequest, "bad JSON body: "+err.Error())
		return false
	}
	return true
}

func status(err error) int {
	switch {
	case errors.Is(err, manager.ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, manager.ErrNotWaiting):
		return http.StatusConflict
	default:
		return http.StatusBadRequest
	}
}

// events streams Event JSON as server-sent events: a hello snapshot
// first, then every change, with a comment line as heartbeat.
func (h *handlers) events(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		server.WriteError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	ch, cancel := h.d.Manager.Subscribe()
	defer cancel()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	send := func(e manager.Event) bool {
		b, err := json.Marshal(e)
		if err != nil {
			return true
		}
		if _, err := fmt.Fprintf(w, "data: %s\n\n", b); err != nil {
			return false
		}
		fl.Flush()
		return true
	}
	if !send(h.d.Manager.Snapshot()) {
		return
	}
	beat := time.NewTicker(h.d.Heartbeat)
	defer beat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case e, ok := <-ch:
			if !ok || !send(e) {
				return
			}
		case <-beat.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			fl.Flush()
		}
	}
}

func (h *handlers) peers(w http.ResponseWriter, _ *http.Request) {
	server.WriteJSON(w, http.StatusOK, h.d.Manager.Peers())
}

func (h *handlers) listTransfers(w http.ResponseWriter, _ *http.Request) {
	server.WriteJSON(w, http.StatusOK, h.d.Manager.Transfers())
}

func (h *handlers) send(w http.ResponseWriter, r *http.Request) {
	var req manager.SendRequest
	if !decode(w, r, &req) {
		return
	}
	ids, err := h.d.Manager.Send(req)
	if err != nil {
		server.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	server.WriteJSON(w, http.StatusAccepted, map[string]any{"ids": ids})
}

func (h *handlers) control(w http.ResponseWriter, r *http.Request) {
	id := contract.TransferID(r.PathValue("id"))
	var err error
	switch r.PathValue("action") {
	case "accept":
		var body struct {
			Trust bool `json:"trust"`
		}
		if r.ContentLength > 0 && !decode(w, r, &body) {
			return
		}
		err = h.d.Manager.Accept(id, body.Trust)
	case "reject":
		err = h.d.Manager.Reject(id)
	case "pause":
		err = h.d.Manager.Pause(id)
	case "resume":
		err = h.d.Manager.Resume(id)
	case "cancel":
		err = h.d.Manager.Cancel(id)
	default:
		server.WriteError(w, http.StatusNotFound, "unknown action")
		return
	}
	if err != nil {
		server.WriteError(w, status(err), err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handlers) listTrust(w http.ResponseWriter, _ *http.Request) {
	server.WriteJSON(w, http.StatusOK, h.d.Crypto.Trusted())
}

func (h *handlers) trust(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if r.ContentLength > 0 && !decode(w, r, &body) {
		return
	}
	id := contract.DeviceID(r.PathValue("id"))
	if err := h.d.Crypto.Trust(contract.TrustedDevice{ID: id, Name: body.Name}); err != nil {
		server.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	h.d.Manager.PeerChanged(id)
	w.WriteHeader(http.StatusNoContent)
}

func (h *handlers) untrust(w http.ResponseWriter, r *http.Request) {
	id := contract.DeviceID(r.PathValue("id"))
	if err := h.d.Crypto.Untrust(id); err != nil {
		server.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	h.d.Manager.PeerChanged(id)
	w.WriteHeader(http.StatusNoContent)
}

func (h *handlers) getSettings(w http.ResponseWriter, _ *http.Request) {
	server.WriteJSON(w, http.StatusOK, h.d.Settings.Get())
}

func (h *handlers) putSettings(w http.ResponseWriter, r *http.Request) {
	s := h.d.Settings.Get() // fields the body leaves out keep their value
	if !decode(w, r, &s) {
		return
	}
	if err := h.d.Settings.Set(s); err != nil {
		server.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	server.WriteJSON(w, http.StatusOK, h.d.Settings.Get())
}

func (h *handlers) listHistory(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	recs, err := h.d.History.List(limit)
	if err != nil {
		server.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if recs == nil {
		recs = []contract.Record{}
	}
	server.WriteJSON(w, http.StatusOK, recs)
}

func (h *handlers) clearHistory(w http.ResponseWriter, _ *http.Request) {
	if err := h.d.History.Clear(); err != nil {
		server.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handlers) roomState() map[string]any {
	members := h.d.Rooms.RoomMembers()
	if members == nil {
		members = []contract.Peer{}
	}
	return map[string]any{"code": h.code, "members": members}
}

func (h *handlers) room(w http.ResponseWriter, _ *http.Request) {
	server.WriteJSON(w, http.StatusOK, h.roomState())
}

func (h *handlers) createRoom(w http.ResponseWriter, r *http.Request) { h.join(w, r, "") }

func (h *handlers) joinRoom(w http.ResponseWriter, r *http.Request) {
	h.join(w, r, r.PathValue("code"))
}

func (h *handlers) join(w http.ResponseWriter, r *http.Request, code string) {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	got, err := h.d.Rooms.JoinRoom(ctx, code)
	if err != nil {
		server.WriteError(w, http.StatusBadGateway, err.Error())
		return
	}
	h.code = got
	h.d.Manager.EmitRoom(got, h.d.Rooms.RoomMembers())
	server.WriteJSON(w, http.StatusOK, h.roomState())
}

func (h *handlers) leaveRoom(w http.ResponseWriter, _ *http.Request) {
	h.d.Rooms.LeaveRoom()
	h.code = ""
	h.d.Manager.EmitRoom("", nil)
	w.WriteHeader(http.StatusNoContent)
}

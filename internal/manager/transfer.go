package manager

import (
	"context"
	"sync"
	"time"

	"github.com/vinayakkam/droplink/internal/contract"
	"github.com/vinayakkam/droplink/internal/engine"
	"github.com/vinayakkam/droplink/internal/files"
)

// transfer is one send or receive. Fields are guarded by Manager.mu.
type transfer struct {
	offer    contract.Offer
	peer     contract.Peer
	outgoing bool
	state    contract.TransferState
	done     int64
	total    int64
	bps      float64
	err      string
	sas      []int
	started  time.Time
	ended    time.Time

	waiting  bool      // an incoming offer waiting for the person
	decision chan bool // their answer
	ctl      *engine.Control
	cancel   context.CancelFunc
	sink     *files.Sink
}

func (t *transfer) finished() bool {
	switch t.state {
	case contract.StateDone, contract.StateFailed, contract.StateCancelled, contract.StateRejected:
		return true
	}
	return false
}

// View is a transfer as the UI sees it.
type View struct {
	ID          contract.TransferID    `json:"id"`
	Peer        contract.Peer          `json:"peer"`
	Outgoing    bool                   `json:"outgoing"`
	State       contract.TransferState `json:"state"`
	Waiting     bool                   `json:"waiting"`
	Files       int                    `json:"files"`
	Names       []string               `json:"names"`
	Text        string                 `json:"text,omitempty"`
	Done        int64                  `json:"done"`
	Total       int64                  `json:"total"`
	BytesPerSec float64                `json:"bytesPerSec"`
	Error       string                 `json:"error,omitempty"`
	SAS         []int                  `json:"sas,omitempty"` // []uint8 would encode as base64
	SavedTo     string                 `json:"savedTo,omitempty"`
	StartedAt   time.Time              `json:"startedAt"`
	EndedAt     *time.Time             `json:"endedAt,omitempty"`
}

func (t *transfer) view() View {
	v := View{
		ID: t.offer.ID, Peer: t.peer, Outgoing: t.outgoing, State: t.state, Waiting: t.waiting,
		Files: len(t.offer.Files), Done: t.done, Total: t.total, BytesPerSec: t.bps,
		Error: t.err, SAS: t.sas, StartedAt: t.started,
	}
	for i, f := range t.offer.Files {
		if i == 3 {
			break
		}
		v.Names = append(v.Names, f.Path)
	}
	// Received text is shown so it can be copied; sent text is not echoed.
	if !t.outgoing && t.state == contract.StateDone {
		v.Text = t.offer.Text
	}
	if !t.ended.IsZero() {
		e := t.ended
		v.EndedAt = &e
	}
	if t.sink != nil && len(t.offer.Files) > 0 && t.state == contract.StateDone {
		if p, ok := t.sink.Saved(t.offer.Files[0].Path); ok {
			v.SavedTo = p
		}
	}
	return v
}

func (t *transfer) record() contract.Record {
	return contract.Record{
		ID: t.offer.ID, Peer: t.peer, Outgoing: t.outgoing, Files: t.offer.Files,
		Text: t.offer.Text != "", State: t.state, Bytes: t.done, StartedAt: t.started, EndedAt: t.ended,
	}
}

// Room is the WebRTC room this device is in.
type Room struct {
	Code    string          `json:"code"`
	Members []contract.Peer `json:"members"`
}

// Event is one update for the UI, sent as JSON over SSE.
//
//	hello:    Peers, Transfers (sent first on every connection)
//	peer:     Kind (joined|updated|left), Peer
//	transfer: Transfer
//	room:     Room (empty code: left the room)
type Event struct {
	Type      string          `json:"type"`
	Kind      string          `json:"kind,omitempty"`
	Peer      *contract.Peer  `json:"peer,omitempty"`
	Peers     []contract.Peer `json:"peers,omitempty"`
	Transfer  *View           `json:"transfer,omitempty"`
	Transfers []View          `json:"transfers,omitempty"`
	Room      *Room           `json:"room,omitempty"`
}

// hub fans events out to subscribers without ever blocking a transfer.
type hub struct {
	mu     sync.Mutex
	subs   map[chan Event]struct{}
	closed bool
}

func newHub() *hub { return &hub{subs: map[chan Event]struct{}{}} }

func (h *hub) subscribe() (<-chan Event, func()) {
	ch := make(chan Event, 64)
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		close(ch)
		return ch, func() {}
	}
	h.subs[ch] = struct{}{}
	h.mu.Unlock()
	var once sync.Once
	return ch, func() {
		once.Do(func() {
			h.mu.Lock()
			if _, ok := h.subs[ch]; ok {
				delete(h.subs, ch)
				close(ch)
			}
			h.mu.Unlock()
		})
	}
}

func (h *hub) emit(e Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs {
		select {
		case ch <- e:
		default: // slow subscriber: it re-syncs from Snapshot on reconnect
		}
	}
}

func (h *hub) close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.closed = true
	for ch := range h.subs {
		delete(h.subs, ch)
		close(ch)
	}
}

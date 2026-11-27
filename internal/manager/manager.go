// Package manager runs transfers for the UI: it sends offers to one or
// many peers through the router (retrying and resuming on drops), accepts
// streams from every transport, asks the person (or the trust list) before
// receiving, and reports everything as events.
package manager

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/vinayakkam/droplink/internal/contract"
	"github.com/vinayakkam/droplink/internal/engine"
	"github.com/vinayakkam/droplink/internal/files"
	"github.com/vinayakkam/droplink/internal/resilience"
)

// Deps are the pieces the manager drives.
type Deps struct {
	Self      contract.Peer
	Crypto    contract.Crypto
	Discovery contract.Discovery
	Router    contract.Router
	History   contract.History
	Engine    contract.Engine
	// Transports to accept incoming streams from (LAN, WebRTC).
	Transports []contract.Transport
	Log        *slog.Logger
}

// Options are the settings the manager reads on every transfer.
type Options struct {
	DownloadDir       func() string
	AutoAcceptTrusted func() bool
	MaxActive         int           // concurrent sends (2)
	DecisionTimeout   time.Duration // an unanswered offer is rejected (5 min)
	Retry             resilience.Policy
}

// Manager owns every transfer of this device.
type Manager struct {
	d    Deps
	opts Options

	mu        sync.Mutex
	transfers map[contract.TransferID]*transfer
	order     []contract.TransferID
	accepted  map[contract.TransferID]bool // offers already accepted (resumes skip the question)

	sem    chan struct{}
	events *hub

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// New returns a Manager; call Run to start accepting.
func New(d Deps, opts Options) *Manager {
	if opts.MaxActive <= 0 {
		opts.MaxActive = 2
	}
	if opts.DecisionTimeout <= 0 {
		opts.DecisionTimeout = 5 * time.Minute
	}
	if opts.AutoAcceptTrusted == nil {
		opts.AutoAcceptTrusted = func() bool { return false }
	}
	if d.Log == nil {
		d.Log = slog.New(slog.DiscardHandler)
	}
	if d.Engine == nil {
		d.Engine = engine.New(64 << 10)
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Manager{
		d: d, opts: opts,
		transfers: map[contract.TransferID]*transfer{},
		accepted:  map[contract.TransferID]bool{},
		sem:       make(chan struct{}, opts.MaxActive),
		events:    newHub(),
		ctx:       ctx, cancel: cancel,
	}
}

// ErrNotFound is returned for an unknown transfer ID.
var ErrNotFound = errors.New("manager: no such transfer")

// ErrNotWaiting is returned when accepting or rejecting a transfer that is
// not waiting for a decision.
var ErrNotWaiting = errors.New("manager: transfer is not waiting for a decision")

func newID() contract.TransferID {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return contract.TransferID(hex.EncodeToString(b))
}

// Run accepts streams from every transport and forwards radar changes
// until Close.
func (m *Manager) Run() {
	for _, t := range m.d.Transports {
		m.wg.Add(1)
		go func() {
			defer m.wg.Done()
			m.acceptLoop(t)
		}()
	}
	if m.d.Discovery != nil {
		ch, cancel := m.d.Discovery.Subscribe()
		m.wg.Add(1)
		go func() {
			defer m.wg.Done()
			defer cancel()
			for {
				select {
				case <-m.ctx.Done():
					return
				case ev, ok := <-ch:
					if !ok {
						return
					}
					p := m.decorate(ev.Peer)
					m.events.emit(Event{Type: "peer", Kind: string(ev.Kind), Peer: &p})
				}
			}
		}()
	}
}

// Close cancels every transfer and waits for the manager's goroutines.
func (m *Manager) Close() {
	m.cancel()
	m.wg.Wait()
	m.events.close()
}

// Subscribe returns the event stream (see Event). Slow subscribers lose
// events; Snapshot gives the full state to start from.
func (m *Manager) Subscribe() (<-chan Event, func()) { return m.events.subscribe() }

// Snapshot is the current peers and transfers.
func (m *Manager) Snapshot() Event {
	m.mu.Lock()
	views := make([]View, 0, len(m.order))
	for _, id := range m.order {
		views = append(views, m.transfers[id].view())
	}
	m.mu.Unlock()
	return Event{Type: "hello", Peers: m.Peers(), Transfers: views}
}

// decorate fills in trust from the trust list.
func (m *Manager) decorate(p contract.Peer) contract.Peer {
	if m.d.Crypto != nil {
		p.Trusted = m.d.Crypto.IsTrusted(p.ID)
	}
	return p
}

// Peers lists devices on the radar (LAN) and in the current room
// (WebRTC), LAN first when a device is on both.
func (m *Manager) Peers() []contract.Peer {
	seen := map[contract.DeviceID]bool{}
	var out []contract.Peer
	if m.d.Discovery != nil {
		for _, p := range m.d.Discovery.Peers() {
			seen[p.ID] = true
			out = append(out, m.decorate(p))
		}
	}
	for _, t := range m.d.Transports {
		if rm, ok := t.(interface{ RoomMembers() []contract.Peer }); ok {
			for _, p := range rm.RoomMembers() {
				if p.ID == m.d.Self.ID || seen[p.ID] {
					continue
				}
				seen[p.ID] = true
				p.Route = contract.RouteWebRTC
				out = append(out, m.decorate(p))
			}
		}
	}
	return out
}

func (m *Manager) findPeer(id contract.DeviceID) (contract.Peer, bool) {
	for _, p := range m.Peers() {
		if p.ID == id {
			return p, true
		}
	}
	return contract.Peer{}, false
}

// SendRequest is what the UI asks to send: files/folders by path, or text.
type SendRequest struct {
	Peers []contract.DeviceID `json:"peers"`
	Paths []string            `json:"paths,omitempty"`
	Text  string              `json:"text,omitempty"`
}

// Send starts one transfer per peer sharing the same file list and
// returns their IDs.
func (m *Manager) Send(req SendRequest) ([]contract.TransferID, error) {
	if len(req.Peers) == 0 {
		return nil, errors.New("manager: pick at least one peer")
	}
	if (len(req.Paths) == 0) == (req.Text == "") {
		return nil, errors.New("manager: send either paths or text")
	}
	if len(req.Text) > 1<<20 {
		return nil, errors.New("manager: text larger than 1 MiB; send it as a file")
	}
	var metas []contract.FileMeta
	var src contract.Source = files.Text{}
	if len(req.Paths) > 0 {
		ms, s, err := files.Walk(req.Paths)
		if err != nil {
			return nil, err
		}
		if len(ms) == 0 {
			return nil, errors.New("manager: nothing to send (empty folders only)")
		}
		metas, src = ms, s
	}
	var targets []contract.Peer
	for _, id := range slices.Compact(slices.Sorted(slices.Values(req.Peers))) {
		p, ok := m.findPeer(id)
		if !ok {
			return nil, fmt.Errorf("manager: %s is not in range", id)
		}
		targets = append(targets, p)
	}
	ids := make([]contract.TransferID, 0, len(targets))
	for _, p := range targets {
		o := contract.Offer{ID: newID(), From: m.d.Self, Files: metas, Text: req.Text}
		tr := m.add(&transfer{offer: o, peer: p, outgoing: true, state: "queued", total: o.TotalSize(),
			ctl: engine.NewControl(), started: time.Now()})
		ids = append(ids, o.ID)
		m.wg.Add(1)
		go func() {
			defer m.wg.Done()
			m.runSend(tr, src)
		}()
	}
	return ids, nil
}

func (m *Manager) runSend(tr *transfer, src contract.Source) {
	ctx, cancel := context.WithCancel(m.ctx)
	defer cancel()
	m.mu.Lock()
	tr.cancel = cancel
	m.mu.Unlock()
	select {
	case m.sem <- struct{}{}:
		defer func() { <-m.sem }()
	case <-ctx.Done():
		m.finish(tr, contract.StateCancelled, ctx.Err())
		return
	}
	err := resilience.Send(engine.WithControl(ctx, tr.ctl), m.opts.Retry, m.d.Router, m.d.Engine, tr.peer,
		tr.offer, src, func(p contract.Progress) { m.progress(tr, p) })
	m.finish(tr, finalState(err), err)
}

func finalState(err error) contract.TransferState {
	switch {
	case err == nil:
		return contract.StateDone
	case errors.Is(err, engine.ErrRejected):
		return contract.StateRejected
	case errors.Is(err, engine.ErrCanceled), errors.Is(err, context.Canceled):
		return contract.StateCancelled
	default:
		return contract.StateFailed
	}
}

func (m *Manager) acceptLoop(t contract.Transport) {
	for {
		s, err := t.Accept(m.ctx)
		if err != nil {
			if m.ctx.Err() != nil {
				return
			}
			select { // transport hiccup: back off, never spin
			case <-m.ctx.Done():
				return
			case <-time.After(200 * time.Millisecond):
			}
			continue
		}
		m.wg.Add(1)
		go func() {
			defer m.wg.Done()
			m.receive(s)
		}()
	}
}

// receive handles one incoming stream from offer to the last byte.
func (m *Manager) receive(s contract.Stream) {
	remote := s.Remote()
	if p, ok := m.findPeer(remote.ID); ok {
		remote.Name, remote.Avatar = p.Name, p.Avatar
	}
	remote = m.decorate(remote)
	var sas []uint8
	if v, ok := s.(interface{ SAS() contract.SAS }); ok {
		x := v.SAS()
		sas = x[:]
	}
	dir := ""
	if m.opts.DownloadDir != nil {
		dir = m.opts.DownloadDir()
	}
	sink, err := files.NewSink(dir)
	if err != nil {
		m.d.Log.Warn("manager: download folder unusable", "dir", dir, "err", err)
		_ = s.Close()
		return
	}

	ctx, cancel := context.WithCancel(m.ctx)
	defer cancel()
	ctl := engine.NewControl()
	var tr *transfer
	accept := func(o contract.Offer) bool {
		o.From = remote // never trust the sender's own description of itself
		m.mu.Lock()
		old, resumed := m.transfers[o.ID]
		again := m.accepted[o.ID] && resumed && !old.outgoing && old.peer.ID == remote.ID
		m.mu.Unlock()
		if again { // the sender retried after a drop: carry on
			tr = old
			m.mu.Lock()
			tr.ctl, tr.cancel, tr.state, tr.err = ctl, cancel, contract.StateActive, ""
			m.mu.Unlock()
			return true
		}
		tr = m.add(&transfer{offer: o, peer: remote, state: contract.StateOffered, total: o.TotalSize(),
			sas: sas, ctl: ctl, cancel: cancel, started: time.Now(), decision: make(chan bool, 1), sink: sink})
		if remote.Trusted && m.opts.AutoAcceptTrusted() {
			m.decide(tr, true)
			return true
		}
		m.mu.Lock()
		tr.waiting = true
		m.mu.Unlock()
		m.emitTransfer(tr)
		timer := time.NewTimer(m.opts.DecisionTimeout)
		defer timer.Stop()
		select {
		case ok := <-tr.decision:
			return ok
		case <-timer.C:
			m.decide(tr, false)
			return false
		case <-ctx.Done():
			return false
		}
	}
	_, err = m.d.Engine.Receive(engine.WithControl(ctx, ctl), s, accept, sink, func(p contract.Progress) {
		if tr != nil {
			m.progress(tr, p)
		}
	})
	if tr == nil {
		return // the stream failed before an offer
	}
	if err != nil && !errors.Is(err, engine.ErrRejected) && resilience.Retryable(err) && m.ctx.Err() == nil {
		// The link dropped: the sender will retry and we resume then.
		m.mu.Lock()
		tr.state, tr.err = contract.StatePaused, "link lost; waiting for the sender to reconnect"
		m.mu.Unlock()
		m.emitTransfer(tr)
		return
	}
	m.finish(tr, finalState(err), err)
}

func (m *Manager) decide(tr *transfer, ok bool) {
	m.mu.Lock()
	tr.waiting = false
	if ok {
		m.accepted[tr.offer.ID] = true
		tr.state = contract.StateActive
	}
	m.mu.Unlock()
	select {
	case tr.decision <- ok:
	default:
	}
	m.emitTransfer(tr)
}

// Accept answers a waiting offer with yes; trust also adds the sender to
// the trust list.
func (m *Manager) Accept(id contract.TransferID, trust bool) error {
	tr, err := m.waiting(id)
	if err != nil {
		return err
	}
	if trust && m.d.Crypto != nil {
		if err := m.d.Crypto.Trust(contract.TrustedDevice{ID: tr.peer.ID, Name: tr.peer.Name}); err != nil {
			return err
		}
	}
	m.decide(tr, true)
	return nil
}

// Reject answers a waiting offer with no.
func (m *Manager) Reject(id contract.TransferID) error {
	tr, err := m.waiting(id)
	if err != nil {
		return err
	}
	m.decide(tr, false)
	return nil
}

func (m *Manager) waiting(id contract.TransferID) (*transfer, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	tr, ok := m.transfers[id]
	if !ok {
		return nil, ErrNotFound
	}
	if !tr.waiting {
		return nil, ErrNotWaiting
	}
	return tr, nil
}

// Pause, Resume and Cancel control a running transfer.
func (m *Manager) Pause(id contract.TransferID) error {
	return m.control(id, func(t *transfer) { t.ctl.Pause() })
}
func (m *Manager) Resume(id contract.TransferID) error {
	return m.control(id, func(t *transfer) { t.ctl.Resume() })
}
func (m *Manager) Cancel(id contract.TransferID) error {
	return m.control(id, func(t *transfer) {
		if t.waiting {
			select {
			case t.decision <- false:
			default:
			}
		}
		if t.cancel != nil {
			t.cancel()
		}
	})
}

func (m *Manager) control(id contract.TransferID, fn func(*transfer)) error {
	m.mu.Lock()
	tr, ok := m.transfers[id]
	m.mu.Unlock()
	if !ok {
		return ErrNotFound
	}
	fn(tr)
	return nil
}

// Transfers lists every transfer of this run, oldest first.
func (m *Manager) Transfers() []View {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]View, 0, len(m.order))
	for _, id := range m.order {
		out = append(out, m.transfers[id].view())
	}
	return out
}

func (m *Manager) add(tr *transfer) *transfer {
	m.mu.Lock()
	m.transfers[tr.offer.ID] = tr
	m.order = append(m.order, tr.offer.ID)
	// Keep the list bounded: drop the oldest finished transfers.
	for len(m.order) > 200 {
		oldest := m.transfers[m.order[0]]
		if !oldest.finished() {
			break
		}
		delete(m.transfers, m.order[0])
		m.order = m.order[1:]
	}
	m.mu.Unlock()
	m.emitTransfer(tr)
	return tr
}

func (m *Manager) progress(tr *transfer, p contract.Progress) {
	m.mu.Lock()
	if tr.finished() {
		m.mu.Unlock()
		return
	}
	tr.done, tr.bps = p.Done, p.BytesPerSec
	if p.Total > 0 {
		tr.total = p.Total
	}
	switch p.State {
	case contract.StateActive, contract.StatePaused, contract.StateOffered:
		if !tr.waiting {
			tr.state = p.State
		}
	}
	m.mu.Unlock()
	m.emitTransfer(tr)
}

func (m *Manager) finish(tr *transfer, state contract.TransferState, err error) {
	m.mu.Lock()
	if tr.finished() {
		m.mu.Unlock()
		return
	}
	tr.state, tr.ended, tr.waiting = state, time.Now(), false
	tr.bps = 0
	if err != nil && state == contract.StateFailed {
		tr.err = err.Error()
	}
	if state == contract.StateDone {
		tr.done = tr.total
	}
	rec := tr.record()
	m.mu.Unlock()
	m.emitTransfer(tr)
	if m.d.History != nil {
		if err := m.d.History.Append(rec); err != nil {
			m.d.Log.Warn("manager: history", "err", err)
		}
	}
}

func (m *Manager) emitTransfer(tr *transfer) {
	m.mu.Lock()
	v := tr.view()
	m.mu.Unlock()
	m.events.emit(Event{Type: "transfer", Transfer: &v})
}

// EmitRoom tells the UI the room changed (the API calls it).
func (m *Manager) EmitRoom(code string, members []contract.Peer) {
	m.events.emit(Event{Type: "room", Room: &Room{Code: code, Members: members}})
}

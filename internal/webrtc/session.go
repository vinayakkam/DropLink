// Package webrtc implements contract.Transport for route "webrtc": devices
// on different networks meet in a signaling room, set up a pion
// PeerConnection, and stream over a reliable DataChannel (DTLS-encrypted).
package webrtc

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	pion "github.com/pion/webrtc/v4"

	"github.com/vinayakkam/droplink/internal/contract"
)

// ErrNoRoom is returned when Dial is called before JoinRoom.
var ErrNoRoom = errors.New("webrtc: not in a room")

// Options tune a Transport. Zero values mean the defaults.
type Options struct {
	ICEServers     []pion.ICEServer // STUN/TURN servers
	ConnectTimeout time.Duration    // per attempt (8 s)
	// IncludeLoopback lets peers on one machine connect over 127.0.0.1
	// (tests and local demos).
	IncludeLoopback bool
	Log             *slog.Logger
}

func (o *Options) defaults() {
	if o.ConnectTimeout <= 0 {
		o.ConnectTimeout = 8 * time.Second
	}
	if o.Log == nil {
		o.Log = slog.New(slog.DiscardHandler)
	}
}

// wire is the Data of every signal this package sends: one session ID
// and either a description or a candidate.
type wire struct {
	SID       string                   `json:"sid"`
	SDP       *pion.SessionDescription `json:"sdp,omitempty"`
	Candidate *pion.ICECandidateInit   `json:"candidate,omitempty"`
}

// Transport sets up WebRTC connections to the members of one room.
type Transport struct {
	self contract.Peer
	sig  contract.Signaler
	opts Options
	api  *pion.API

	mu       sync.Mutex
	room     contract.Room
	loopDone chan struct{}
	sessions map[string]*session // setting up, by session ID

	// accepted receives each connection the other side opened.
	accepted func(pc *pion.PeerConnection, dc *pion.DataChannel, from contract.DeviceID)

	done chan struct{}
	wg   sync.WaitGroup
}

// New returns a Transport that signals through sig.
func New(self contract.Peer, sig contract.Signaler, opts Options) *Transport {
	opts.defaults()
	var se pion.SettingEngine
	se.DetachDataChannels() // DataChannels become io.ReadWriteClosers
	se.SetIncludeLoopbackCandidate(opts.IncludeLoopback)
	self.Route = contract.RouteWebRTC
	t := &Transport{
		self:     self,
		sig:      sig,
		opts:     opts,
		api:      pion.NewAPI(pion.WithSettingEngine(se)),
		sessions: map[string]*session{},
		done:     make(chan struct{}),
	}
	t.accepted = func(pc *pion.PeerConnection, _ *pion.DataChannel, _ contract.DeviceID) { _ = pc.Close() }
	return t
}

// JoinRoom joins (code "") or creates a signaling room and leaves any
// previous one. It returns the room code.
func (t *Transport) JoinRoom(ctx context.Context, code string) (string, error) {
	t.LeaveRoom()
	room, err := t.sig.Join(ctx, code, t.self)
	if err != nil {
		return "", err
	}
	done := make(chan struct{})
	t.mu.Lock()
	t.room, t.loopDone = room, done
	t.mu.Unlock()
	t.wg.Add(1)
	go func() {
		defer t.wg.Done()
		defer close(done)
		for s := range room.Signals() {
			t.dispatch(room, s)
		}
	}()
	return room.Code(), nil
}

// LeaveRoom leaves the current room. Open streams stay up; connections
// still being set up fail.
func (t *Transport) LeaveRoom() {
	t.mu.Lock()
	room, done := t.room, t.loopDone
	t.room, t.loopDone = nil, nil
	pending := make([]*session, 0, len(t.sessions))
	for _, s := range t.sessions {
		pending = append(pending, s)
	}
	t.mu.Unlock()
	if room == nil {
		return
	}
	_ = room.Close()
	<-done
	for _, s := range pending {
		s.fail()
	}
}

// RoomMembers lists who is in the room (us included), or nil.
func (t *Transport) RoomMembers() []contract.Peer {
	t.mu.Lock()
	room := t.room
	t.mu.Unlock()
	if room == nil {
		return nil
	}
	return room.Members()
}

func (t *Transport) currentRoom() contract.Room {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.room
}

// dispatch routes one signal from the room.
func (t *Transport) dispatch(room contract.Room, s contract.Signal) {
	if s.Kind == contract.SignalBye {
		t.mu.Lock()
		var gone []*session
		for _, ss := range t.sessions {
			if ss.peer == s.From {
				gone = append(gone, ss)
			}
		}
		t.mu.Unlock()
		for _, ss := range gone {
			ss.fail()
		}
		return
	}
	var w wire
	if s.Kind == contract.SignalHello || json.Unmarshal(s.Data, &w) != nil || w.SID == "" {
		return
	}
	t.mu.Lock()
	ss := t.sessions[w.SID]
	t.mu.Unlock()
	switch s.Kind {
	case contract.SignalOffer:
		if ss != nil || w.SDP == nil {
			return
		}
		t.wg.Add(1)
		go func() {
			defer t.wg.Done()
			t.onOffer(room, s.From, w)
		}()
	case contract.SignalAnswer:
		if ss != nil && ss.peer == s.From && w.SDP != nil {
			select {
			case ss.answer <- *w.SDP:
			default:
			}
		}
	case contract.SignalCandidate:
		if ss != nil && ss.peer == s.From && w.Candidate != nil {
			ss.addRemote(*w.Candidate)
		}
	}
}

// session is one PeerConnection while it is being set up.
type session struct {
	t    *Transport
	sid  string
	peer contract.DeviceID
	room contract.Room
	pc   *pion.PeerConnection

	mu        sync.Mutex
	sentDesc  bool                    // our offer/answer has gone out
	outQueue  []pion.ICECandidateInit // our candidates found before that
	remoteSet bool
	inQueue   []pion.ICECandidateInit // theirs, before their description

	answer    chan pion.SessionDescription
	connected chan struct{}
	failed    chan struct{}
	once      sync.Once
	failOnce  sync.Once
}

func newSID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (t *Transport) newSession(room contract.Room, sid string, peer contract.DeviceID, policy pion.ICETransportPolicy) (*session, error) {
	pc, err := t.api.NewPeerConnection(pion.Configuration{ICEServers: t.opts.ICEServers, ICETransportPolicy: policy})
	if err != nil {
		return nil, err
	}
	s := &session{
		t: t, sid: sid, peer: peer, room: room, pc: pc,
		answer:    make(chan pion.SessionDescription, 1),
		connected: make(chan struct{}),
		failed:    make(chan struct{}),
	}
	pc.OnICECandidate(func(c *pion.ICECandidate) {
		if c == nil {
			return // gathering finished
		}
		init := c.ToJSON()
		s.mu.Lock()
		if !s.sentDesc {
			s.outQueue = append(s.outQueue, init)
			s.mu.Unlock()
			return
		}
		s.mu.Unlock()
		s.send(contract.SignalCandidate, wire{SID: sid, Candidate: &init})
	})
	pc.OnICEConnectionStateChange(func(st pion.ICEConnectionState) {
		switch st {
		case pion.ICEConnectionStateConnected, pion.ICEConnectionStateCompleted:
			s.once.Do(func() { close(s.connected) })
		case pion.ICEConnectionStateFailed, pion.ICEConnectionStateClosed:
			s.fail()
		}
	})
	t.mu.Lock()
	t.sessions[sid] = s
	t.mu.Unlock()
	return s, nil
}

func (s *session) fail() { s.failOnce.Do(func() { close(s.failed) }) }

// forget stops routing signals to s.
func (s *session) forget() {
	s.t.mu.Lock()
	if s.t.sessions[s.sid] == s {
		delete(s.t.sessions, s.sid)
	}
	s.t.mu.Unlock()
}

// abort closes the connection after a failed setup.
func (s *session) abort() {
	s.forget()
	_ = s.pc.Close()
}

func (s *session) send(kind contract.SignalKind, w wire) {
	b, err := json.Marshal(w)
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.room.Send(ctx, contract.Signal{Kind: kind, To: s.peer, Data: b}); err != nil {
		s.t.opts.Log.Debug("webrtc: signal not sent", "kind", kind, "err", err)
	}
}

// sendDesc sends our offer or answer, then the candidates found so far.
func (s *session) sendDesc(kind contract.SignalKind, d pion.SessionDescription) {
	s.send(kind, wire{SID: s.sid, SDP: &d})
	s.mu.Lock()
	s.sentDesc = true
	queued := s.outQueue
	s.outQueue = nil
	s.mu.Unlock()
	for i := range queued {
		s.send(contract.SignalCandidate, wire{SID: s.sid, Candidate: &queued[i]})
	}
}

// setRemote applies their description, then candidates that came early.
func (s *session) setRemote(d pion.SessionDescription) error {
	if err := s.pc.SetRemoteDescription(d); err != nil {
		return err
	}
	s.mu.Lock()
	s.remoteSet = true
	queued := s.inQueue
	s.inQueue = nil
	s.mu.Unlock()
	for _, c := range queued {
		_ = s.pc.AddICECandidate(c)
	}
	return nil
}

func (s *session) addRemote(c pion.ICECandidateInit) {
	s.mu.Lock()
	if !s.remoteSet {
		s.inQueue = append(s.inQueue, c)
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()
	_ = s.pc.AddICECandidate(c)
}

// offer opens a connection to peer and returns it once its DataChannel is
// open. On error everything it created is closed.
func (t *Transport) offer(ctx context.Context, peer contract.DeviceID, policy pion.ICETransportPolicy) (*pion.PeerConnection, *pion.DataChannel, error) {
	room := t.currentRoom()
	if room == nil {
		return nil, nil, ErrNoRoom
	}
	s, err := t.newSession(room, newSID(), peer, policy)
	if err != nil {
		return nil, nil, err
	}
	ordered := true
	dc, err := s.pc.CreateDataChannel("droplink", &pion.DataChannelInit{Ordered: &ordered})
	if err != nil {
		s.abort()
		return nil, nil, err
	}
	opened := make(chan struct{})
	var openOnce sync.Once
	dc.OnOpen(func() { openOnce.Do(func() { close(opened) }) })

	desc, err := s.pc.CreateOffer(nil)
	if err == nil {
		err = s.pc.SetLocalDescription(desc)
	}
	if err != nil {
		s.abort()
		return nil, nil, err
	}
	s.sendDesc(contract.SignalOffer, desc)

	timer := time.NewTimer(t.opts.ConnectTimeout)
	defer timer.Stop()
	wait := func(ch <-chan struct{}) error {
		select {
		case <-ch:
			return nil
		case <-s.failed:
			return errors.New("webrtc: connection failed")
		case <-timer.C:
			return errTimeout
		case <-ctx.Done():
			return ctx.Err()
		case <-t.done:
			return ErrClosed
		}
	}
	select {
	case ans := <-s.answer:
		if err := s.setRemote(ans); err != nil {
			s.abort()
			return nil, nil, fmt.Errorf("webrtc: bad answer: %w", err)
		}
	case <-s.failed:
		s.abort()
		return nil, nil, errors.New("webrtc: peer left before answering")
	case <-timer.C:
		s.abort()
		return nil, nil, errTimeout
	case <-ctx.Done():
		s.abort()
		return nil, nil, ctx.Err()
	case <-t.done:
		s.abort()
		return nil, nil, ErrClosed
	}
	if err := wait(opened); err != nil {
		s.abort()
		return nil, nil, err
	}
	s.forget()
	return s.pc, dc, nil
}

// onOffer answers an offer and hands the opened DataChannel to accepted.
func (t *Transport) onOffer(room contract.Room, from contract.DeviceID, w wire) {
	s, err := t.newSession(room, w.SID, from, pion.ICETransportPolicyAll)
	if err != nil {
		return
	}
	opened := make(chan *pion.DataChannel, 1)
	s.pc.OnDataChannel(func(dc *pion.DataChannel) {
		dc.OnOpen(func() {
			select {
			case opened <- dc:
			default:
			}
		})
	})
	if err := s.setRemote(*w.SDP); err != nil {
		s.abort()
		return
	}
	desc, err := s.pc.CreateAnswer(nil)
	if err == nil {
		err = s.pc.SetLocalDescription(desc)
	}
	if err != nil {
		s.abort()
		return
	}
	s.sendDesc(contract.SignalAnswer, desc)

	// The offerer may fall back to a relay attempt, so give it two tries.
	timer := time.NewTimer(2*t.opts.ConnectTimeout + time.Second)
	defer timer.Stop()
	select {
	case dc := <-opened:
		s.forget()
		t.accepted(s.pc, dc, from)
	case <-s.failed:
		s.abort()
	case <-timer.C:
		s.abort()
	case <-t.done:
		s.abort()
	}
}

var (
	errTimeout = errors.New("webrtc: no connection within the timeout")
	// ErrClosed is returned by a closed Transport.
	ErrClosed = errors.New("webrtc: transport closed")
)

package webrtc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pion/datachannel"
	pion "github.com/pion/webrtc/v4"

	"github.com/vinayakkam/droplink/internal/contract"
)

// Backpressure: a Write waits while more than highWater bytes are queued
// in the DataChannel and resumes once it drains to lowWater, so memory per
// stream stays bounded however fast the sender reads from disk.
const (
	maxMessage = 16 << 10 // bytes per DataChannel message
	highWater  = 1 << 20
	lowWater   = 256 << 10
	readBuf    = 64 << 10 // ≥ any message a DropLink peer sends
	drainWait  = 2 * time.Second
)

// stream is one detached DataChannel on its own PeerConnection.
type stream struct {
	pc     *pion.PeerConnection
	dc     *pion.DataChannel
	raw    datachannel.ReadWriteCloser
	remote contract.Peer

	rmu     sync.Mutex
	rbuf    []byte
	pending []byte

	wmu    sync.Mutex
	low    chan struct{} // signaled when the buffer drains to lowWater
	closed chan struct{}
	once   sync.Once
	peak   atomic.Uint64 // highest BufferedAmount seen after a write (tests)

	t *Transport
}

func (t *Transport) newStream(pc *pion.PeerConnection, dc *pion.DataChannel, remote contract.Peer) (*stream, error) {
	raw, err := dc.Detach()
	if err != nil {
		_ = pc.Close()
		return nil, fmt.Errorf("webrtc: detach: %w", err)
	}
	remote.Route = contract.RouteWebRTC
	s := &stream{
		pc: pc, dc: dc, raw: raw, remote: remote,
		rbuf:   make([]byte, readBuf),
		low:    make(chan struct{}, 1),
		closed: make(chan struct{}),
		t:      t,
	}
	dc.SetBufferedAmountLowThreshold(lowWater)
	dc.OnBufferedAmountLow(func() {
		select {
		case s.low <- struct{}{}:
		default:
		}
	})
	// The connection dropping ends the stream, which unblocks Read.
	pc.OnConnectionStateChange(func(st pion.PeerConnectionState) {
		if st == pion.PeerConnectionStateFailed || st == pion.PeerConnectionStateDisconnected {
			_ = s.Close()
		}
	})
	return s, nil
}

func (s *stream) Remote() contract.Peer { return s.remote }

// Read returns bytes from DataChannel messages, keeping any part of a
// message that did not fit p for the next call.
func (s *stream) Read(p []byte) (int, error) {
	s.rmu.Lock()
	defer s.rmu.Unlock()
	for len(s.pending) == 0 {
		n, err := s.raw.Read(s.rbuf)
		if err != nil {
			select {
			case <-s.closed:
				return 0, io.EOF
			default:
			}
			if errors.Is(err, io.ErrShortBuffer) {
				return 0, fmt.Errorf("webrtc: message larger than %d bytes", readBuf)
			}
			return 0, err
		}
		s.pending = s.rbuf[:n]
	}
	n := copy(p, s.pending)
	s.pending = s.pending[n:]
	return n, nil
}

// Write sends p in messages of at most 16 KiB, waiting whenever the
// channel's send buffer is above 1 MiB.
func (s *stream) Write(p []byte) (int, error) {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	written := 0
	for len(p) > 0 {
		for s.dc.BufferedAmount() > highWater {
			select {
			case <-s.low:
			case <-s.closed:
				return written, net.ErrClosed
			case <-time.After(time.Second): // re-check; never trust one callback
			}
		}
		n := min(len(p), maxMessage)
		if _, err := s.raw.Write(p[:n]); err != nil {
			return written, err
		}
		if b := s.dc.BufferedAmount(); b > s.peak.Load() {
			s.peak.Store(b)
		}
		written += n
		p = p[n:]
	}
	return written, nil
}

// Close ends the stream and its PeerConnection; pending Read and Write
// calls return.
func (s *stream) Close() error {
	var err error
	s.once.Do(func() {
		close(s.closed) // ends Write waits first
		// Let queued messages reach the peer before tearing down; a
		// stuck peer gets drainWait, not forever.
		deadline := time.Now().Add(drainWait)
		for s.dc.BufferedAmount() > 0 && time.Now().Before(deadline) &&
			s.pc.ConnectionState() == pion.PeerConnectionStateConnected {
			time.Sleep(5 * time.Millisecond)
		}
		err = s.raw.Close()
		if cerr := s.pc.Close(); err == nil {
			err = cerr
		}
		s.t.forget(s)
	})
	return err
}

// Dial opens a stream to a member of the current room.
func (t *Transport) Dial(ctx context.Context, to contract.Peer) (contract.Stream, error) {
	select {
	case <-t.done:
		return nil, ErrClosed
	default:
	}
	pc, dc, err := t.offer(ctx, to.ID, pion.ICETransportPolicyAll)
	if err != nil {
		return nil, err
	}
	s, err := t.newStream(pc, dc, to)
	if err != nil {
		return nil, err
	}
	t.track(s)
	return s, nil
}

func (t *Transport) track(s *stream) {
	t.mu.Lock()
	t.streams[s] = struct{}{}
	t.mu.Unlock()
}

func (t *Transport) forget(s *stream) {
	t.mu.Lock()
	delete(t.streams, s)
	t.mu.Unlock()
}

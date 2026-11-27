package webrtc

import (
	"context"

	pion "github.com/pion/webrtc/v4"

	"github.com/vinayakkam/droplink/internal/contract"
)

var _ contract.Transport = (*Transport)(nil)

// maxPending is how many opened streams may wait for Accept before new
// ones are refused.
const maxPending = 16

// startAccepting makes answered connections show up in Accept. New calls
// it; a Transport nobody accepts from still refuses beyond maxPending.
func (t *Transport) startAccepting() {
	t.incoming = make(chan contract.Stream, maxPending)
	t.accepted = func(pc *pion.PeerConnection, dc *pion.DataChannel, from contract.DeviceID) {
		remote := contract.Peer{ID: from}
		for _, m := range t.RoomMembers() {
			if m.ID == from {
				remote = m
			}
		}
		s, err := t.newStream(pc, dc, remote)
		if err != nil {
			return
		}
		t.track(s)
		select {
		case t.incoming <- s:
		default:
			_ = s.Close()
		}
	}
}

func (t *Transport) Route() contract.Route { return contract.RouteWebRTC }

// Accept returns the next stream another member opened to us.
func (t *Transport) Accept(ctx context.Context) (contract.Stream, error) {
	select {
	case s := <-t.incoming:
		return s, nil
	case <-t.done:
		return nil, ErrClosed
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Close leaves the room, closes every stream and waits for the goroutines
// the transport started.
func (t *Transport) Close() error {
	t.closeOnce.Do(func() {
		close(t.done)
		t.LeaveRoom()
		t.mu.Lock()
		open := make([]*stream, 0, len(t.streams))
		for s := range t.streams {
			open = append(open, s)
		}
		t.mu.Unlock()
		for _, s := range open {
			_ = s.Close()
		}
		t.wg.Wait()
		for {
			select {
			case s := <-t.incoming:
				_ = s.Close()
			default:
				return
			}
		}
	})
	return nil
}

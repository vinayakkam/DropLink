package webrtc

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	pion "github.com/pion/webrtc/v4"

	"github.com/vinayakkam/droplink/internal/contract"
)

// DefaultICE is used when DROPLINK_ICE is not set: one public STUN server,
// enough for most home networks. Add a TURN server for strict NATs.
var DefaultICE = []pion.ICEServer{{URLs: []string{"stun:stun.l.google.com:19302"}}}

// ICEFromEnv parses DROPLINK_ICE, a JSON list such as
//
//	[{"urls":["stun:stun.example.org:3478"]},
//	 {"urls":["turn:turn.example.org:3478"],"username":"u","credential":"p"}]
func ICEFromEnv() ([]pion.ICEServer, error) {
	v := os.Getenv("DROPLINK_ICE")
	if v == "" {
		return slices.Clone(DefaultICE), nil
	}
	var servers []pion.ICEServer
	if err := json.Unmarshal([]byte(v), &servers); err != nil {
		return nil, fmt.Errorf("webrtc: DROPLINK_ICE: %w", err)
	}
	for _, s := range servers {
		if len(s.URLs) == 0 {
			return nil, errors.New("webrtc: DROPLINK_ICE entry without urls")
		}
		for _, u := range s.URLs {
			if !strings.HasPrefix(u, "stun:") && !strings.HasPrefix(u, "turn:") && !strings.HasPrefix(u, "turns:") {
				return nil, fmt.Errorf("webrtc: DROPLINK_ICE: %q is not a stun/turn URL", u)
			}
		}
	}
	return servers, nil
}

// hasTURN reports whether a relay fallback is possible.
func hasTURN(servers []pion.ICEServer) bool {
	for _, s := range servers {
		for _, u := range s.URLs {
			if strings.HasPrefix(u, "turn:") || strings.HasPrefix(u, "turns:") {
				return true
			}
		}
	}
	return false
}

// connect tries a direct (or STUN-assisted) path first. If nothing
// connects within the timeout and a TURN server is configured, it tries
// again relay-only, which works through any NAT the relay can reach.
func (t *Transport) connect(ctx context.Context, to contract.DeviceID) (*pion.PeerConnection, *pion.DataChannel, error) {
	t.attempt(pion.ICETransportPolicyAll)
	pc, dc, err := t.offer(ctx, to, pion.ICETransportPolicyAll)
	if !errors.Is(err, errTimeout) || !hasTURN(t.opts.ICEServers) {
		return pc, dc, err
	}
	t.opts.Log.Info("webrtc: no direct path, trying the relay", "peer", to)
	t.attempt(pion.ICETransportPolicyRelay)
	return t.offer(ctx, to, pion.ICETransportPolicyRelay)
}

func (t *Transport) attempt(p pion.ICETransportPolicy) {
	if t.opts.onAttempt != nil {
		t.opts.onAttempt(p)
	}
}

// sasFor derives the 4 glyphs from both DTLS certificate fingerprints in
// the negotiated descriptions. Both ends see the same two fingerprints, so
// they get the same glyphs; a man in the middle would have to present its
// own certificates, which changes them.
func sasFor(pc *pion.PeerConnection) contract.SAS {
	var fps []string
	for _, d := range []*pion.SessionDescription{pc.LocalDescription(), pc.RemoteDescription()} {
		if d != nil {
			fps = append(fps, fingerprints(d.SDP)...)
		}
	}
	slices.Sort(fps)
	h := sha256.New()
	h.Write([]byte("droplink webrtc sas"))
	for _, f := range fps {
		h.Write([]byte{0})
		h.Write([]byte(f))
	}
	sum := h.Sum(nil)
	return contract.SAS{sum[0] & 63, sum[1] & 63, sum[2] & 63, sum[3] & 63}
}

func fingerprints(sdp string) []string {
	var out []string
	for _, line := range strings.Split(sdp, "\n") {
		if f, ok := strings.CutPrefix(strings.TrimSpace(line), "a=fingerprint:"); ok {
			out = append(out, strings.ToLower(f))
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// SAS is the 4-glyph code for this stream (type-assert for
// interface{ SAS() contract.SAS }).
func (s *stream) SAS() contract.SAS { return s.sas }

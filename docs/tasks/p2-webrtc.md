# p2/webrtc: WebRTC DataChannel transport
Owner: **Kashish** · Base: `p2/signaling` · Needs: p2/signaling

## Goal
Implement `contract.Transport` for route `webrtc` with pion: devices on different networks connect through a room and stream files over a DataChannel, with a TURN relay when a direct path fails.

## Files
`internal/webrtc/` (new), `go.mod`/`go.sum` (`github.com/pion/webrtc/v4`), `internal/app/providers.go` (**only** the `p2/webrtc` line).

## Design
- One `PeerConnection` per peer, one ordered reliable DataChannel per `Stream`, **detached** (`SettingEngine.DetachDataChannels`) so it is an `io.ReadWriteCloser`.
- Backpressure: writes wait while `BufferedAmount` > 1 MiB, resume at `BufferedAmountLowThreshold` 256 KiB. Memory per stream stays bounded.
- Signaling through `contract.Signaler` / `Room`: offer, answer, trickle candidates.
- ICE servers from `DROPLINK_ICE` (JSON list of `{urls, username, credential}`); default one public STUN server.
- Relay fallback: if no connected candidate pair within 8 s, restart ICE with `ICETransportPolicyRelay` (needs a TURN entry).
- DTLS already encrypts; SAS comes from both DTLS fingerprints (hash → 4 × 6 bits) so the glyph check works on this route too.

## Commits
1. `feat(webrtc): add peer connection setup over a signaling room`
2. `feat(webrtc): add detached DataChannel streams with backpressure`
3. `feat(webrtc): add ICE config, relay fallback and SAS`
4. `feat(webrtc): add Accept loop and wire providers`
5. `test(webrtc): ...`: two in-process peers over `mock.Hub` move 32 MiB; buffered amount never above 1 MiB; Close unblocks; relay-only policy chosen after timeout (fake); leak check.

## Done when
- Two sidecars on different machines, same room, complete a transfer (manual; note it in the report).
- `task check` passes.

# p2/lan-transfer: encrypted TCP transport
Owner: **Kashish** · Base: `p2/lan-discovery` · Needs: p2/lan-discovery

## Goal
Implement `contract.Transport` for route `lan`: TCP streams, secured by `contract.Crypto`, with optional zstd compression and a speed limit.

## Files
`internal/lan/` (new), `go.mod`/`go.sum` (`github.com/klauspost/compress`), `internal/app/providers.go` (**only** the `p2/lan-transfer` line).

## Design
- Listener on all interfaces, random port (or `DROPLINK_LAN_PORT`); its port goes into the discovery beacon.
- `Dial`: TCP with a 5 s timeout and keep-alive, then `Crypto.Secure`; fail if the remote ID is not the peer's ID.
- `Accept`: same, as responder. Handshakes run with a deadline so a silent client cannot hold a goroutine.
- Compression negotiated per stream in the first byte after the handshake: zstd at the fastest level, `WithEncoderConcurrency(1)`, small window (RAM). Skip it when the sender says the data is already compressed.
- Speed limit: a token-bucket `io.Writer` wrapper, limit from settings (0 = none).
- `Close` stops the listener and every open stream.

## Commits
1. `feat(lan): add TCP listener and dialer with timeouts`
2. `feat(lan): secure streams with the crypto handshake`
3. `feat(lan): add negotiated zstd compression`
4. `feat(lan): add speed limiter and wire providers`
5. `test(lan): ...`: two transports on loopback move 64 MiB; wrong peer ID refused; compression round trip; limiter accuracy ±10 %; Close unblocks Accept/Read; leak check.

## Done when
- A real transfer between two sidecars on loopback (after p1/ui-features) works without mocks for LAN.
- `task check` passes.

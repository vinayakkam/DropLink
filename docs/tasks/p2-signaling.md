# p2/signaling: WebSocket signaling hub and client
Owner: **Kashish** · Base: `p2/lan-transfer` · Needs: p1/scaffold

## Goal
A tiny signaling server that relays WebRTC setup messages between devices in a room and **stores nothing**, plus the `contract.Signaler` client the sidecar uses.

There is no signaling code in PrivMeet to port (it is a meeting-notes app); write this from scratch. The relay-only, in-memory design is the "PrivMeet spirit": nothing kept, nothing logged.

## Files
`cmd/droplink-signal/` (new), `internal/signal/` (new), `go.mod`/`go.sum` (`github.com/coder/websocket`), `internal/app/providers.go` (**only** the `p2/signaling` line).

## Design
- Server: `GET /ws?room=<code>` upgrades to WebSocket. Room codes: 6 chars from `ABCDEFGHJKMNPQRSTUVWXYZ23456789`. `room` empty = create.
- Messages are JSON `contract.Signal`. The hub sets `From`, routes by `To`, never stores or logs `Data`.
- Limits: 8 members per room, 64 KiB per message, 20 messages/s per connection, rooms vanish 10 min after the last message, ping every 20 s.
- Client: `signal.New(url)` implements `contract.Signaler`; reconnects with backoff while the room lives. URL from `DROPLINK_SIGNAL_URL` (default `ws://127.0.0.1:47480` for tests).
- `cmd/droplink-signal -addr :47480`: about 10 MB RAM idle; graceful shutdown.

## Commits
1. `feat(signal): add room registry with codes, limits and expiry`
2. `feat(signal): add WebSocket relay server`
3. `feat(signal): add Signaler client with reconnect`
4. `feat(signal): add droplink-signal command and wire providers`
5. `test(signal): ...`: httptest server, two clients exchange offer/answer, `To` routing, member and size limits, rate limit, expiry with a fake clock, nothing left in memory after all leave, leak check.

## Done when
- `go run ./cmd/droplink-signal` + two sidecars with `DROPLINK_SIGNAL_URL` can create and join a room.
- `task check` passes.

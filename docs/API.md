# Sidecar API

The Go sidecar serves this API on `127.0.0.1:<port>`; the desktop shell gets the port and token from the ready line and hands them to the UI through the `sidecar_info` command.

- Every route except `/api/health` needs `Authorization: Bearer <token>` (or `?token=<token>` for `EventSource`).
- `Host` must be a loopback name; other hosts get `403`.
- Errors: `{"error": "message"}` with a 4xx/5xx status.
- A route's **feature** is listed in `GET /api/info` once that route exists. The UI checks it before showing a control.

## Available now (p1/scaffold)
| Method | Path | Returns |
|---|---|---|
| GET | `/api/health` | `{"ok": true, "version": "0.1.0"}` (no token) |
| GET | `/api/info` | `{"version", "device", "uptimeSec", "chunkKiB", "features": []}` |
| GET | `/dev/session` | `{"token"}`, only with `-web-dir` (browser development) |

## Available now (p1/ui-features)
| Method | Path | Feature | Body → Returns |
|---|---|---|---|
| GET | `/api/events` | `events` | SSE: `data: Event` per message (see below), `: ping` every 15 s |
| GET | `/api/peers` | `peers` | `[Peer]` (LAN radar + room members, with `trusted`) |
| GET | `/api/transfers` | `send` | `[Transfer]`, oldest first |
| POST | `/api/transfers` | `send` | `{"peers": [id], "paths": [string]}` or `{"peers": [id], "text": string}` → `202 {"ids": [id]}` |
| POST | `/api/transfers/{id}/accept` | `control` | `{"trust": bool}` (optional) → `204`; `409` if not waiting |
| POST | `/api/transfers/{id}/reject` · `pause` · `resume` · `cancel` | `control` | `204`; `404` unknown ID |
| GET | `/api/rooms` | `rooms` | `{"code", "members": [Peer]}` |
| POST | `/api/rooms` | `rooms` | create → `{"code", "members"}` |
| POST | `/api/rooms/{code}/join` | `rooms` | `{"code", "members"}`; `502` if the hub refuses |
| DELETE | `/api/rooms/current` | `rooms` | `204` |
| GET | `/api/trust` | `trust` | `[TrustedDevice]` |
| PUT · DELETE | `/api/trust/{id}` | `trust` | `{"name"}` (PUT) → `204` |
| GET · PUT | `/api/settings` | `settings` | `Settings` (PUT merges, validates, saves, applies) |
| GET · DELETE | `/api/history` | `history` | `?limit=` (≤ 500) → `[Record]` newest first |

### Events
`{"type": "hello", "peers": [...], "transfers": [...]}` first (empty lists are omitted), then `peer` (`kind`: joined/updated/left, `peer`), `transfer` (`transfer`: Transfer) and `room` (`room`: `{code, members}`, empty code after leaving).

`Transfer`: `{id, peer, outgoing, state, waiting, files, names[≤3], text (received text, when done), done, total, bytesPerSec, error, sas[4], savedTo, startedAt, endedAt}`. `state` is `queued`, `offered`, `active`, `paused`, `done`, `failed`, `cancelled` or `rejected`.

`Settings`: `{name, avatar 0..15, downloadDir (absolute), autoAcceptTrusted, speedLimitKBps (0 = none), volume 0..100, theme cyan|amber|magenta, motion, history, closeToTray}`.

## Planned
| Method | Path | Feature | Branch | Body → Returns |
|---|---|---|---|---|
| GET | `/api/rooms/{code}/qr` | `qr` | p1/desktop-integration | `{"size": n, "bits": "0101…"}` (pixel-drawn by the UI) |

Types are the JSON forms of the structs in `internal/contract/contract.go`, `internal/manager` and `internal/api`.

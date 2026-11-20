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

## Planned
| Method | Path | Feature | Branch | Body → Returns |
|---|---|---|---|---|
| GET | `/api/events` | `events` | p1/ui-features | SSE stream: `peer`, `offer`, `progress`, `room`, `sas` events |
| GET | `/api/peers` | `peers` | p1/ui-features | `[Peer]` |
| POST | `/api/transfers` | `send` | p1/ui-features | `{"peers": [id], "paths": [string]}` or `{"peers": [id], "text": string}` → `{"ids": [id]}` |
| GET | `/api/transfers` | `send` | p1/ui-features | `[Transfer]` (active and queued) |
| POST | `/api/transfers/{id}/accept` | `send` | p1/ui-features | `{"dir": string?}` → `204` |
| POST | `/api/transfers/{id}/reject` | `send` | p1/ui-features | `204` |
| POST | `/api/transfers/{id}/pause` · `/resume` · `/cancel` | `control` | p1/ui-features | `204` |
| POST | `/api/rooms` | `rooms` | p1/ui-features | `{}` → `{"code"}` (create) |
| POST | `/api/rooms/{code}/join` | `rooms` | p1/ui-features | `204` |
| DELETE | `/api/rooms/{code}` | `rooms` | p1/ui-features | `204` |
| GET | `/api/rooms/{code}/qr` | `qr` | p1/desktop-integration | `{"size": n, "bits": "0101…"}` (pixel-drawn by the UI) |
| GET | `/api/trust` | `trust` | p1/ui-features | `[TrustedDevice]` |
| PUT | `/api/trust/{id}` · DELETE | `trust` | p1/ui-features | `204` |
| GET · PUT | `/api/settings` | `settings` | p1/ui-features | `Settings` |
| GET · DELETE | `/api/history` | `history` | p1/ui-features | `[Record]` |

Types are the JSON forms of the structs in `internal/contract/contract.go`.

# DropLink: task split

Two people, six branches each. Every branch is **4 feature commits + 1 test commit**, commit only, no push (see `AGENTS.md`, "Task protocol"). The person reviews, pushes and merges.

| Person | Name | Area |
|---|---|---|
| P1 | **Vinayak** | transfer engine, sidecar API, pixel UI, desktop shell |
| P2 | **Kashish** | crypto, LAN discovery, LAN transport, signaling, WebRTC, resilience |

## Branches
| # | Branch | Owner | Base | Needs | Task file |
|---|---|---|---|---|---|
| 1 | `p1/scaffold` | Vinayak | `main` | none | [p1-scaffold](tasks/p1-scaffold.md) |
| 2 | `p1/transfer-engine` | Vinayak | `p1/scaffold` | p1/scaffold | [p1-transfer-engine](tasks/p1-transfer-engine.md) |
| 3 | `p1/ui-shell` | Vinayak | `p1/transfer-engine` | p1/scaffold | [p1-ui-shell](tasks/p1-ui-shell.md) |
| 4 | `p1/pixel-engine` | Vinayak | `p1/ui-shell` | p1/ui-shell | [p1-pixel-engine](tasks/p1-pixel-engine.md) |
| 5 | `p1/ui-features` | Vinayak | `p1/pixel-engine` | p1/transfer-engine, p1/pixel-engine | [p1-ui-features](tasks/p1-ui-features.md) |
| 6 | `p1/desktop-integration` | Vinayak | `p1/ui-features` | p1/ui-features | [p1-desktop-integration](tasks/p1-desktop-integration.md) |
| 7 | `p2/crypto` | Kashish | `p1/scaffold` | p1/scaffold | [p2-crypto](tasks/p2-crypto.md) |
| 8 | `p2/lan-discovery` | Kashish | `p2/crypto` | p2/crypto | [p2-lan-discovery](tasks/p2-lan-discovery.md) |
| 9 | `p2/lan-transfer` | Kashish | `p2/lan-discovery` | p2/lan-discovery | [p2-lan-transfer](tasks/p2-lan-transfer.md) |
| 10 | `p2/signaling` | Kashish | `p2/lan-transfer` | p1/scaffold | [p2-signaling](tasks/p2-signaling.md) |
| 11 | `p2/webrtc` | Kashish | `p2/signaling` | p2/signaling | [p2-webrtc](tasks/p2-webrtc.md) |
| 12 | `p2/resilience` | Kashish | `p2/webrtc` | p2/lan-transfer, p2/webrtc | [p2-resilience](tasks/p2-resilience.md) |

- Each person's branches **stack**: a branch starts from the one before it, so nothing waits for a merge. Once a base is merged into `main`, start from `main` instead.
- P1 and P2 never wait for each other. Both build against `internal/contract` and its mocks in `internal/mock`.
- Merge order into `main`: `p1/scaffold` first, then any order; each P2 branch only swaps its own line in `internal/app/providers.go`.

## Status
| Branch | State |
|---|---|
| `p1/scaffold` | in progress |
| everything else | not started |

## Ownership
Every path has one owner. Ask the owner before editing their files.

| Path | Owner |
|---|---|
| `cmd/droplink-sidecar/`, `internal/config/`, `internal/server/`, `internal/app/` | Vinayak (P2 edits only its marked line in `providers.go`) |
| `internal/contract/` | **both**: change only when both agree |
| `internal/mock/` | Vinayak |
| `internal/engine/`, `internal/files/`, `internal/manager/`, `internal/api/` | Vinayak |
| `web/`, `desktop/`, `tools/`, `Taskfile.yml` | Vinayak |
| `internal/crypto/` | Kashish |
| `internal/discovery/` | Kashish |
| `internal/lan/` | Kashish |
| `internal/signal/`, `cmd/droplink-signal/` | Kashish |
| `internal/webrtc/` | Kashish |
| `internal/resilience/`, `internal/history/` | Kashish |
| `AGENTS.md`, `docs/TASKS.md`, `docs/API.md`, `README.md` | Vinayak (Kashish edits own rows/sections) |
| `docs/tasks/p1-*.md` / `docs/tasks/p2-*.md` | Vinayak / Kashish |

## Contracts (so parallel work fits together)
- **Go interfaces:** `internal/contract/contract.go`. Every one has a mock in `internal/mock`.
  - `Crypto`, `SecureConn`, `SAS` → p2/crypto
  - `Discovery`, `PeerEvent` → p2/lan-discovery
  - `Transport`, `Stream` (route `lan`) → p2/lan-transfer
  - `Signaler`, `Room`, `Signal` → p2/signaling
  - `Transport`, `Stream` (route `webrtc`) → p2/webrtc
  - `Router`, `History`, `Record` → p2/resilience
  - `Engine`, `Source`, `Sink`, `Offer`, `Progress` → p1/transfer-engine
- **Wiring:** `internal/app/providers.go`. Each P2 branch replaces exactly its one marked line. P1 never edits those lines.
- **Configuration for P2 code** comes from env vars read inside P2's own package (`DROPLINK_SIGNAL_URL`, `DROPLINK_ICE`, `DROPLINK_LAN_PORT`), so `internal/config` stays P1's.
- **Sidecar API:** `docs/API.md`. JSON in, JSON out, errors `{"error": "..."}`. Routes appear in `GET /api/info` `features`; the UI shows a control only when its feature is listed.
- **Ready line:** `DROPLINK_READY {"port","token","version"}` on stdout (`internal/server.ReadyPrefix`, `desktop/src-tauri/src/sidecar.rs`).
- **Streams:** implementations never buffer a whole file. Close unblocks Read and Write.
- **SAS glyphs:** `SAS` is 4 values 0..63; the UI draws glyph *n* with `web/js/pixel/glyphs.js` (p1/pixel-engine). Both ends must derive the same 4 values from the handshake transcript.

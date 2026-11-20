# p1/desktop-integration: tray, OS integration, QR, README
Owner: **Vinayak** · Base: `p1/ui-features` · Needs: p1/ui-features

## Goal
Make it feel native: tray, notifications, real drag-drop and pickers, clipboard, QR and deep links to join rooms, and the full README.

## Files
`desktop/`, `web/`, `internal/api/` (QR route), `go.mod`/`go.sum` (`rsc.io/qr`), `README.md`, `docs/ARCHITECTURE.md`, `tools/mem/`, `Taskfile.yml`.

## Design
- Tray: show/hide, "receive mode" toggle, quit. Close button hides to tray when the setting is on.
- `tauri-plugin-notification`: incoming offer, transfer done/failed (only when the window is not focused).
- Window drag-drop event gives real paths (folders too) → `POST /api/transfers`. `tauri-plugin-dialog` for file/folder pickers and the download folder.
- `tauri-plugin-clipboard-manager`: "Send clipboard" button; received text gets a "Copy" button.
- `GET /api/rooms/{code}/qr` returns QR modules; the UI draws them as pixel art. `tauri-plugin-deep-link`: `droplink://join/<CODE>` opens the app and joins.
- `tools/mem`: prints working set / private bytes of the shell, webview and sidecar (`task mem`).

## Commits
1. `feat(desktop): add tray, close-to-tray and notifications`
2. `feat(desktop): add native drag-drop, pickers and clipboard share`
3. `feat(desktop): add room QR codes and droplink:// deep links`
4. `docs: add README, architecture and memory tool`
5. `test(desktop): ...`: Rust tests for deep-link parsing and tray state; Go test for the QR route; `task mem` numbers recorded in README.

## Done when
- A release build installs, sits in the tray, and a `droplink://join/ABC234` link joins that room.
- `task check` passes.

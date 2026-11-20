# p1/ui-features: transfer manager, API routes and screens
Owner: **Vinayak** · Base: `p1/pixel-engine` · Needs: p1/transfer-engine, p1/pixel-engine

## Goal
Everything a user does: see peers, send files/folders/text to one or many peers, accept with SAS check, pause/resume/cancel, rooms, trust list, settings, themes, sounds, history. Runs on the mocks until P2 lands.

## Files
`internal/manager/` (new), `internal/api/` (new), `cmd/droplink-sidecar/main.go` (mount routes), `web/`, `docs/API.md` (move rows to "Available").

## Design
- `internal/manager`: queue (max 2 active sends, configurable), dials through `contract.Router`, accepts from both transports, runs `contract.Engine`, fans progress out to SSE subscribers, writes `contract.History`. Multi-peer send = one transfer per peer sharing one file list. Clipboard share = `Offer.Text`.
- `internal/api`: routes from `docs/API.md` "Planned" (except `/qr`), SSE `/api/events` with a 15 s heartbeat, settings in `DataDir/settings.json` (atomic write).
- UI: radar click → select peers → drop/pick files → send; incoming offer dialog with 4 SAS glyphs and "trust this device"; cargo log with live drone progress; rooms screen (create shows the code big, join takes a code); history; settings (name, avatar, download folder, auto-accept trusted, speed limit, sound, theme, motion).
- Themes: **Cyan Nebula** (default), **Amber Terminal**, **Magenta Void**, by swapping CSS variables only.
- Sounds: WebAudio square/triangle blips synthesized in code (no audio files): select, send, incoming, done, error. Volume setting, default 40 %, 0 = off.

## Commits
1. `feat(manager): add transfer manager over router and engine`
2. `feat(api): add peers, transfers, rooms, trust, settings and SSE routes`
3. `feat(ui): wire radar, send flow, offer dialog and cargo log`
4. `feat(ui): add settings, themes, sounds and history screens`
5. `test(api): ...`: two managers on one `mock.Network` send a folder end to end; httptest for every route and auth; SSE event order; node tests for UI state reducers.

## Done when
- With `task web`, two browser tabs on two sidecars (different `-data-dir`) can complete a mock transfer.
- `task check` passes.

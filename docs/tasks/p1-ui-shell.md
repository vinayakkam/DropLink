# p1/ui-shell: sci-fi pixel interface shell
Owner: **Vinayak** · Base: `p1/transfer-engine` · Needs: p1/scaffold

## Goal
The whole frame of the app in plain HTML/CSS/JS: tokens, pixel font, CRT layer, custom title bar, layout, a tiny state store and view switcher. No canvas art yet (p1/pixel-engine).

## Files
`web/` (all), `desktop/src-tauri/tauri.conf.json` (window `decorations: false`), `desktop/src-tauri/capabilities/default.json` (window controls), `Taskfile.yml` (`test:web`).

## Design
- `web/css/tokens.css`: colors as CSS variables (void, panel, cyan, magenta, amber, danger), spacing on a 4 px grid, pixel borders via stepped `box-shadow`.
- Font: Monocraft (OFL, from PrivMeet `frontend/src/ui/fonts/`), license file kept next to it.
- CRT layer: scanlines + vignette + slight glow; flicker off for reduced motion.
- Layout: title bar (drag region, min/max/close) · left **Radar** panel · center **Docking Bay** (drop zone) · right **Cargo Log** (transfers) · bottom status bar (engine, route, speed).
- `web/js/store.js` (observable state, ~60 lines), `web/js/views.js` (hash views: `#/`, `#/rooms`, `#/history`, `#/settings`), `web/js/format.js` (bytes, speed, ETA).
- Boot sequence screen (typed lines) while the engine starts.
- Keyboard: every control reachable by Tab, visible focus ring.

## Commits
1. `feat(ui): add design tokens, pixel font and base styles`
2. `feat(ui): add CRT layer and custom title bar`
3. `feat(ui): add radar, docking bay and cargo log layout`
4. `feat(ui): add state store, views and boot sequence`
5. `test(ui): ...`: `node --test web/tests` for store, views, format; a Go test that `web/` has no external URLs or inline scripts (CSP).

## Done when
- Window has no OS frame, drags by the title bar, min/max/close work.
- 860×560 minimum size still fits every panel.
- `task check` passes.

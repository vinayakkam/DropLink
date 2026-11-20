# p1/scaffold: sidecar, Tauri shell, contracts, agent kit
Owner: **Vinayak** · Base: `main` · Needs: none

## Goal
A Tauri 2 app that starts a Go sidecar, a loopback API with a token, the fixed contracts with mocks, and the setup any AI agent can work from.

## Files
`go.mod`, `.gitignore`, `.gitattributes`, `cmd/droplink-sidecar/`, `internal/{config,server,contract,mock,app}/`, `desktop/`, `web/index.html`, `web/js/{api,boot}.js`, `tools/`, `Taskfile.yml`, `AGENTS.md`, `CLAUDE.md`, `opencode.json`, `.claude/`, `.opencode/`, `docs/`.

## Commits
1. `feat(scaffold): add Go sidecar with loopback API and ready line`: config (defaults → env → flags, loopback only), token auth, Host check, CORS for Tauri origins, `/api/health`, `/api/info`, `-watch-stdin`, memory limit.
2. `feat(scaffold): add Tauri v2 shell that runs the Go sidecar`: `sidecar.rs` (spawn, ready line, kill on quit), `sidecar_info` command, single instance, `tools/buildsidecar`, pixel icon, boot page.
3. `feat(scaffold): add shared contracts, mocks and provider wiring`: `internal/contract`, `internal/mock`, `internal/app/providers.go`.
4. `docs(scaffold): add agent kit, task docs and Taskfile`: `AGENTS.md`, start-task commands, `docs/TASKS.md`, `docs/API.md`, 12 task files, `Router`/`History` contracts.
5. `test(scaffold): ...`: config, server middleware, mocks, sidecar run loop, Rust ready-line parser.

## Done when
- `task dev` opens the window and it shows "ENGINE: ONLINE".
- Killing the app leaves no `droplink-sidecar` process.
- `task check` passes.

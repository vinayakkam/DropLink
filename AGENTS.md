# DropLink: agent rules

Rules for every AI coding agent in this repo (Claude Code, OpenCode, Codex, Cursor, others).

DropLink sends files peer to peer, over the LAN or WebRTC. No server stores anything.
- Desktop app: Tauri 2 shell (`desktop/`) + Go sidecar engine (`cmd/droplink-sidecar`, `internal/`) + plain-JS sci-fi pixel UI (`web/`).
- Task index, ownership, contracts: `docs/TASKS.md`. One task = one file in `docs/tasks/`.
- Status: all 12 planned branches are merged into `main` (PRs #1-#12). New work gets a task file in `docs/tasks/` and a row in `docs/TASKS.md` first.
- Sidecar HTTP API: `docs/API.md`. Go contracts: `internal/contract/contract.go`.

## Team
| Person | Name | Owns |
|---|---|---|
| P1 | Vinayak | engine, UI, desktop shell (`p1/*` branches) |
| P2 | Kashish | crypto, discovery, transports, signaling, resilience (`p2/*` branches) |

## Communication style
Caveman. No articles, no filler, no pleasantries. Short. Direct. Code speaks.

## Saving tokens
- Read only: `AGENTS.md`, your task file in `docs/tasks/`, and the files it lists. Read `docs/TASKS.md` only for ownership or contract questions.
- Never read `desktop/src-tauri/target/`, `desktop/node_modules/`, `desktop/src-tauri/binaries/`, `*.lock`, `desktop/src-tauri/icons/`, `docs/ui/*.jpg`.
- Run `task check` (short output) instead of separate gofmt/vet/test calls.
- Search with grep for a symbol before opening whole files.

## Task protocol (follow exactly)
1. **Wait for "start".** Never pick, start or continue a task on your own. Begin only when the person says e.g. "start p2/crypto" (or `/start-task p2/crypto Kashish`).
2. **Know who is speaking.** No name given: ask "Vinayak or Kashish?". Task belongs to the other person: say so and ask before going on.
3. **Read the task file** `docs/tasks/<branch with / as ->.md` (e.g. `docs/tasks/p2-crypto.md`). Check its "Needs": each needed branch is merged into `main`. Missing: stop, say which.
4. **Branch from fresh `main`:**
   ```
   git checkout main
   git pull --ff-only
   git checkout -b <branch>
   ```
   Dirty tree: stop and ask. Never stash, reset or discard someone's changes.
5. **Stay in scope.** Edit only the files the task lists. Ask before touching anything else, above all the other person's area ("Ownership" in `docs/TASKS.md`).
   - P2 branches change exactly one marked line each in `internal/app/providers.go`.
   - Never change `internal/contract` alone. Contract changes need both people.
6. **Check your work** against "Done when". Run `task check`. Report what you could not check.
7. **Exactly 5 commits per branch:**
   - Commits 1–4: the four feature steps the task file lists, in order. Each builds and runs on its own. No empty, whitespace-only or "wip" commits.
   - Commit 5: `test(<scope>): ...`, the tests for the whole branch. All tests pass after it.
   - `git add <specific files>`. Never add `binaries/`, `target/`, `node_modules/`, `.env`, keys.
   - Message: subject `feat(<scope>): short description` (or `fix`/`test`/`docs`/`chore`), max 72 chars; blank line; body saying **what** changed, **why**, and **how it was checked**, wrapped at 72.
   - The git identity already configured on the machine. Do not change git config.
   - **Kashish's (P2) work committed from Vinayak's machine** ends with `Co-Authored-By: kashish1809 <kashishaternos@gmail.com>`. P1 commits have no trailer.
   - **No AI or agent attribution of any kind**: no AI `Co-Authored-By`, no "Generated with", in commits or PR bodies.
   - `git add` only this step's files and check `git status` before every commit: the index must hold nothing else.
   - Never rewrite pushed history (no force-push). A fix for a pushed branch goes into the next branch.
   - Small fixes outside the plan (a bug, a docs update) may be fewer commits: one per logical change, tests included.
8. **Ship it** once `task check` passes:
   ```
   git push -u origin <branch>
   gh pr create --base main --head <branch> --title "..." --body "..."   # what, why, how checked
   gh pr merge <number> --merge
   ```
   Vinayak's standing rule: push, open a PR and merge every finished branch. The next branch starts from the updated `main`.
9. **Report** branch, every commit hash + subject, the PR link, what you verified, what is left.

## Project rules
- **Low RAM, low CPU.**
  - Stream everything. No `io.ReadAll`, no whole-file buffers. Reuse chunk buffers (`sync.Pool`).
  - Every goroutine has an owner that stops it (context or close). Tests check for leaks.
  - The sidecar runs under a 64 MiB soft memory limit. Keep it.
  - UI animation: 30 fps cap, pauses when hidden or idle, off for `prefers-reduced-motion`. Static scenery is drawn once to an offscreen canvas.
- **Dependencies: ask first.** Already approved:
  - Go: `golang.org/x/crypto` (p2/crypto), `github.com/coder/websocket` (p2/signaling), `github.com/pion/webrtc/v4` (p2/webrtc), `github.com/klauspost/compress` (p2/lan-transfer), `rsc.io/qr` (p1/desktop-integration).
  - Rust: `tauri` (with `tray-icon`) + plugins `single-instance` (with `deep-link`), `notification`, `dialog`, `clipboard-manager`, `deep-link`.
  - JS: none. No framework, no bundler, no TypeScript, no npm runtime deps.
- **Security.** The sidecar API binds to loopback only and needs the per-run token; CORS allows only the Tauri and loopback origins. Never log the token, keys or file contents. Paths from peers are untrusted: reject absolute paths and `..`.
- **UI.** Original pixel art only (no sprites, names or looks from existing games). Colors come from CSS variables in `web/css/tokens.css`; the three themes only redefine them. Font: Monocraft (OFL).
  - Text goes into the DOM only through `textContent` (`web/js/ui/dom.js`), never `innerHTML`.
  - No Go files in `web/`: Tauri bundles that folder. UI checks live in `tools/webcheck`.
  - Check UI changes in the real desktop window too (`task dev`), not only in a browser (`task web`): origins and WebView2 rules differ.
- **Go style.** `gofmt`, `go vet`, errors wrapped with `%w`, JSON errors as `{"error": "..."}` via `server.WriteError`.
- A known shortcut gets a comment: `// shortcut: <limit>, <when to upgrade>`.

## Commands
```
task            # list tasks
task check      # gofmt + vet + staticcheck + all tests
task test       # Go + UI (node --test) + Rust tests
task test:web   # UI tests only
task dev        # build sidecar, run the desktop app with hot reload
task web        # sidecar serving web/ at http://127.0.0.1:7878 (browser dev, no Tauri)
task build      # release installers
task mem        # memory of a running app (shell, sidecar, WebView)
```
Needs Go 1.26+ (`golang.org/x/crypto` requires it), Rust stable, Node 20+, `task` (`go install github.com/go-task/task/v3/cmd/task@latest`), `staticcheck` (`go install honnef.co/go/tools/cmd/staticcheck@latest`) and `gh` for pull requests.

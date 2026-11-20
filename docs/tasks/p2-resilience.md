# p2/resilience: routing, retries, network changes, history
Owner: **Kashish** · Base: `p2/webrtc` · Needs: p2/lan-transfer, p2/webrtc

## Goal
Implement `contract.Router` and `contract.History`: pick the best route per peer, retry and resume after drops, react to network changes, and keep an opt-in transfer log.

## Files
`internal/resilience/` (new), `internal/history/` (new), `internal/app/providers.go` (**only** the two `p2/resilience` lines).

## Design
- Router order: LAN if discovery saw the peer in the last 7 s, else WebRTC (direct, then relay). Remember the last route that worked per peer.
- Retry: jittered exponential backoff (0.5 s → 8 s, max 6 tries) for network errors only; never retry a reject or cancel. The engine resumes from the receiver's `.part` offsets, so a retry continues instead of restarting.
- Network watcher: poll interfaces every 5 s; on change, reset cached routes and tell discovery to announce at once.
- History: `DataDir/history.jsonl`, newest at the end, capped at 500 records (rewrite on cap), no file contents, no clipboard text. Only written when the history setting is on.

## Commits
1. `feat(resilience): add route selection across LAN and WebRTC`
2. `feat(resilience): add retry policy with jittered backoff`
3. `feat(resilience): add network change watcher`
4. `feat(history): add capped JSONL history and wire providers`
5. `test(resilience): ...`: router prefers LAN, falls back to WebRTC; retry stops on non-retryable errors; a dropped mock stream resumes and finishes; history cap and order; corrupted history line skipped; leak check.

## Done when
- Pulling the network cable mid-transfer and plugging it back resumes the transfer (manual).
- `task check` passes.

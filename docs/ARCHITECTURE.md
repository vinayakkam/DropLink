# DropLink architecture

This document describes how DropLink is built: its processes, how a transfer flows through them, the formats on the wire, and how memory and failures are handled. The HTTP API between the UI and the engine is in [API.md](API.md).

## Processes

| Process | Language | Job | Lifetime |
|---|---|---|---|
| `droplink` (shell) | Rust, Tauri 2 | window, tray, notifications, native pickers, clipboard, deep links; starts and stops the sidecar | the app |
| WebView | system (WebView2 / WebKit) | runs the UI from `web/` | the window |
| `droplink-sidecar` | Go | discovery, transports, crypto, transfers, settings, history, the local API | child of the shell |
| `droplink-signal` | Go | relays WebRTC setup messages between room members | optional, on a server |

The shell starts the sidecar with `-watch-stdin` and its own data folder, reads one line from its stdout, `DROPLINK_READY {"port","token","version"}`, and gives port and token to the UI through the `sidecar_info` command. The sidecar exits when its stdin closes, so it never outlives the shell, even after a crash; the shell also kills it on quit.

## Inside the sidecar

```
            UI (HTTP + SSE, token)
                     │
               internal/api ─────────── settings.json
                     │
              internal/manager ──────── history.jsonl (opt-in)
               │            │
   resilience.Router     accept loops
     │        │             │      │
   lan     webrtc  ◀────────┘      │
     │        │                    │
   crypto   signal ── hub          │
     │                             │
  discovery                 internal/engine ── internal/files (disk)
```

Every box below the manager implements an interface in `internal/contract`. Each has an in-memory version in `internal/mock`, which is what let the two halves of the project be built in parallel. `internal/app/providers.go` picks the real implementations.

## A transfer, end to end

1. **Discovery.** Every device sends a UDP beacon every 2 s to `239.255.42.99:47474` and to each subnet's broadcast address. A beacon carries the device ID, name, ship, LAN port and a per-run nonce. Peers silent for 7 s leave the radar.
2. **Send.** The UI posts peer IDs and paths. The manager walks the paths once (folders keep their structure, symlinks are skipped) and starts one transfer per peer.
3. **Route.** The router uses the LAN if discovery heard from the peer in the last 7 s, otherwise WebRTC. It prefers the route that last worked for that peer.
4. **Connect.**
   - *LAN:* TCP, then the crypto handshake, then an optional zstd stream.
   - *WebRTC:* offer, answer and trickle ICE through the room, then a detached DataChannel.
5. **Offer.** The engine sends the file list. The receiver's manager asks the person, with the 4 SAS glyphs, unless the sender is trusted and auto-accept is on.
6. **Answer.** The receiver answers with per-file resume offsets, taken from any `.droplink-part` files it already has.
7. **Data.** Chunks of 64 KiB, each file closed with its SHA-256. The receiver hashes as it writes, including the prefix it already had, and renames the part file only when the hash matches.
8. **Done.** The receiver confirms and both sides record the result. A dropped link is retried with backoff, from 0.5 s up to 8 s and at most 6 tries; the retry resumes from the receiver's part file.

## Wire formats

### Engine frames (any stream)
`type u8 | length u32 BE | payload`

| Type | Payload |
|---|---|
| OFFER | JSON `{id, from, files[{path,size,modTime}], text}` (≤ 4 MiB, ≤ 100 000 files) |
| ANSWER | JSON `{accept, offsets[]}` |
| DATA | ≤ one chunk of file bytes |
| FILE_END | 32-byte SHA-256 of the whole file |
| DONE | empty (sender: all sent; receiver: all committed) |
| PAUSE / RESUME / CANCEL | empty, either direction |
| ERROR | reason text |

### LAN handshake and records
```
1  I→R  "DLH1" | eph_I (32) | static_I (32)
2  R→I  eph_R (32) | static_R (32) | Sign_R("resp" | h)
3  I→R  Sign_I("init" | h)          h = SHA-256(label | msg1 | eph_R | static_R)
keys = HKDF-SHA256(X25519(eph_I, eph_R), salt h): one per direction + 4 SAS bytes
record = len u16 | ChaCha20-Poly1305(counter nonce, ≤ 16 KiB)
```
After the handshake, one feature byte negotiates zstd. The dialer proposes it, and the listener accepts it only if its own settings allow.

### Beacon (UDP, ≤ 113 bytes)
`"DLNK" | version | avatar | port u16 | nonce u64 | id (32 hex) | name length | name`

### Signaling (WebSocket)
- Clients send `contract.Signal` JSON.
- The hub answers with `{"type":"welcome","code","members"}`, `{"type":"signal","signal"}` or `{"type":"error","error"}`.
- The hub stamps `From` itself, relays only offer, answer and candidate, caps messages at 64 KiB and 20 per second, and holds rooms in memory only.

## Memory and CPU

| Where | Bound |
|---|---|
| Engine | one pooled chunk buffer per transfer; no `io.ReadAll`; a 256 MiB transfer stays under 8 MiB of heap (test) |
| Crypto | two fixed record buffers per connection; 0 allocations per record (benchmark) |
| LAN zstd | fastest level, 1 MiB window, one goroutine per stream |
| WebRTC | writes wait above 1 MiB queued and resume at 256 KiB |
| Sidecar | 64 MiB Go soft memory limit |
| Hub | 32 MiB soft limit; 8 members/room, 10 000 rooms |
| UI | 30 fps cap; static layers drawn once offscreen; the loop stops when hidden or after 60 s idle; reduced motion draws one frame per change |

Every goroutine has an owner that stops it: a context, a `Close`, or the end of its stream. The tests fail if a test ends with more goroutines than it started with.

## Failure handling

| Failure | What happens |
|---|---|
| Wi-Fi drops mid-transfer | the sender retries with backoff; the receiver keeps the part file and resumes; the UI shows "paused: waiting for the sender to reconnect" |
| Network changes (new Wi-Fi, VPN) | discovery rejoins the group and announces at once; the router forgets cached routes |
| A byte is corrupted | the file's SHA-256 does not match: the part file is deleted and the transfer fails (a retry starts that file clean) |
| Peer stops reading | backpressure holds the sender; cancel or close ends the wait |
| Wrong device answers a LAN dial | handshake identity check fails (`ErrWrongPeer`) |
| Shell crashes | the sidecar sees stdin close and exits |
| Hub disappears | room clients retry with backoff and give up when the room is gone; LAN keeps working |
| Settings file damaged | reported at start, never overwritten |

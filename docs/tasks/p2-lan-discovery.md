# p2/lan-discovery: find peers on the local network
Owner: **Kashish** · Base: `p2/crypto` · Needs: p2/crypto

## Goal
Implement `contract.Discovery` with UDP multicast: peers appear on the radar within 2 s and drop off about 7 s after they go quiet.

## Files
`internal/discovery/` (new), `internal/app/providers.go` (**only** the `p2/lan-discovery` line).

## Design
- Beacon (≤ 512 bytes, versioned): magic `DLNK`, version, device ID, name, avatar, TCP port of the LAN transport, a random instance nonce.
- Multicast group `239.255.42.99:47474` on every up, multicast-capable interface; also send to the subnet broadcast address as a fallback.
- Announce every 2 s and immediately when name/avatar/port change. Ignore our own beacons (same device ID).
- Peer table with a 7 s TTL. `Subscribe` fans events out without blocking (drop for slow subscribers).
- Re-scan interfaces every 5 s and re-join groups when they change (Wi-Fi switch, VPN).
- Port of the LAN transport: from `DROPLINK_LAN_PORT` or set by p2/lan-transfer through a setter.

## Commits
1. `feat(discovery): add beacon encoding with size and version checks`
2. `feat(discovery): add multicast announce and listen loops`
3. `feat(discovery): add peer table with TTL and event fan-out`
4. `feat(discovery): rejoin on interface changes and wire providers`
5. `test(discovery): ...`: beacon fuzz (no panic), TTL expiry with a fake clock, two instances see each other on loopback (skip if multicast is unavailable), slow subscriber never blocks, leak check.

## Done when
- Two sidecars on the same Wi-Fi show each other in `GET /api/peers` (needs p1/ui-features) or in a test.
- `task check` passes.

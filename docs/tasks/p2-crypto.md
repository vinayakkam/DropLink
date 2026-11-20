# p2/crypto: device identity, encrypted sessions, trust
Owner: **Kashish** · Base: `p1/scaffold` · Needs: p1/scaffold

## Goal
Implement `contract.Crypto` and `contract.SecureConn`: every LAN connection is authenticated and encrypted, both screens show the same 4-glyph SAS, and trusted devices are remembered.

## Files
`internal/crypto/` (new), `go.mod`/`go.sum` (`golang.org/x/crypto`), `internal/app/providers.go` (**only** the `p2/crypto` line).

## Design
- Identity: Ed25519 key pair in `DataDir/identity.key` (file mode 0600, created on first run). `DeviceID` = hex of the first 16 bytes of SHA-256(public key).
- Handshake (both sides): X25519 ephemeral keys + Ed25519 signatures over the transcript hash. Keys from HKDF-SHA256, one key per direction.
- Records: `len u16 | ChaCha20-Poly1305(seq nonce, data)`, max 16 KiB plaintext, sequence-number nonces (no reuse; reject replays or reordering). Reuse buffers; no per-record allocation on the hot path.
- SAS: 4 × 6 bits from HKDF(transcript, "droplink sas") → `contract.SAS`.
- Trust list: `DataDir/trusted.json`, written atomically (temp file + rename).
- Wiring: replace the `p2/crypto` line with `crypto.Load(cfg.DataDir)`.

## Commits
1. `feat(crypto): add Ed25519 device identity on disk`
2. `feat(crypto): add X25519 handshake with signed transcript`
3. `feat(crypto): add ChaCha20-Poly1305 records, SAS and SecureConn`
4. `feat(crypto): add trust store and wire it into providers`
5. `test(crypto): ...`: handshake over `net.Pipe`; same SAS both ends; flipped byte → error; replayed record → error; wrong identity → error; trust survives restart; `BenchmarkSecureConn` with `-benchmem`.

## Done when
- `go test ./internal/crypto` passes and the benchmark shows 0 allocs per record.
- `task check` passes.

# p1/transfer-engine: streaming transfer engine
Owner: **Vinayak** · Base: `p1/scaffold` · Needs: p1/scaffold

## Goal
`contract.Engine`: send an `Offer`'s files over any `contract.Stream` in fixed chunks with constant memory, verify each file with SHA-256, resume after an interruption, pause and cancel.

## Files
`internal/engine/` (new), `internal/files/` (new). Nothing else.

## Design
- Frames: `type u8 | len u32 | payload`. Types: `OFFER`, `ANSWER` (accept/reject + per-file resume offsets), `DATA`, `FILE_END` (sha256), `DONE`, `PAUSE`, `RESUME`, `CANCEL`, `ERROR`. Max payload = chunk size (`-chunk`, default 64 KiB) + header.
- Buffers from one `sync.Pool`; the hot path allocates nothing per chunk.
- Sender hashes the whole file (prefix it skips included), receiver hashes its existing prefix through `io.ReaderAt` (see `contract.Sink`).
- Progress callback at most 5 times per second, with an EWMA speed.
- `internal/files`: disk `Source` (walks folders into relative slash paths) and `Sink` (`.part` files, resume, rename on Commit, unique name on conflict, rejects absolute paths and `..`).

## Commits
1. `feat(engine): add frame codec with size limits`
2. `feat(engine): stream files with pooled buffers and SHA-256`
3. `feat(files): add disk source and resumable sink`
4. `feat(engine): add pause, cancel, resume and progress rate`
5. `test(engine): ...`: round trip over `mock.Pipe`, resume mid-file, corrupted byte → `Abort`, path traversal fuzz, cancel from both sides, leak check, `BenchmarkSend` with `-benchmem` (0 allocs/chunk).

## Done when
- 1 GiB synthetic transfer over `mock.Pipe` keeps heap under 8 MiB (benchmark reports it).
- `task check` passes.

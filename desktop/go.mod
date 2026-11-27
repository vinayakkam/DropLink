// Not Go code. This file marks desktop/ as a separate Go module so that
// `go vet ./...` and `go test ./...` at the repo root never walk into
// Tauri's build output (src-tauri/target) or node_modules.
module droplink.invalid/desktop

go 1.26

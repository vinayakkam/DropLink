// Command gofmtcheck fails when any Go file is not gofmt-formatted.
// `gofmt -l` alone exits 0 even when it lists files.
package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
)

func main() {
	out, err := exec.Command("gofmt", "-l", "cmd", "internal", "tools").CombinedOutput()
	if err != nil {
		fmt.Fprintf(os.Stderr, "gofmt: %v\n%s", err, out)
		os.Exit(1)
	}
	if len(bytes.TrimSpace(out)) > 0 {
		fmt.Fprintf(os.Stderr, "not gofmt-formatted (run gofmt -w):\n%s", out)
		os.Exit(1)
	}
}

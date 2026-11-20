// Package testutil has helpers shared by tests in every package.
package testutil

import (
	"runtime"
	"testing"
	"time"
)

// NoLeaks fails the test if it ends with more goroutines than it started
// with. A dependency-free stand-in for goleak: call it first in a test.
func NoLeaks(t testing.TB) {
	t.Helper()
	before := runtime.NumGoroutine()
	t.Cleanup(func() {
		deadline := time.Now().Add(2 * time.Second)
		for {
			n := runtime.NumGoroutine()
			if n <= before {
				return
			}
			if time.Now().After(deadline) {
				buf := make([]byte, 1<<16)
				buf = buf[:runtime.Stack(buf, true)]
				t.Errorf("goroutine leak: %d before, %d after\n%s", before, n, trim(string(buf)))
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	})
}

func trim(s string) string {
	if len(s) > 4000 {
		return s[:4000] + "\n..."
	}
	return s
}

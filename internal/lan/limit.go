package lan

import (
	"io"
	"net"
	"os"
	"strconv"
	"sync"
	"time"
)

// limiter is a token bucket shared by every stream of a transport, so the
// speed limit applies to the device as a whole.
type limiter struct {
	mu     sync.Mutex
	rate   int64 // bytes per second; 0 = unlimited
	tokens float64
	last   time.Time
	now    func() time.Time
}

func newLimiter() *limiter { return &limiter{now: time.Now} }

func (l *limiter) set(rate int64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if rate < 0 {
		rate = 0
	}
	l.rate, l.tokens, l.last = rate, 0, l.now()
}

// reserve takes n bytes and returns how long to wait before sending them.
// Debt is allowed, so a large write waits once instead of starving.
func (l *limiter) reserve(n int) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.rate == 0 {
		return 0
	}
	now := l.now()
	burst := float64(max(l.rate/4, 64<<10))
	l.tokens = min(burst, l.tokens+now.Sub(l.last).Seconds()*float64(l.rate))
	l.last = now
	l.tokens -= float64(n)
	if l.tokens >= 0 {
		return 0
	}
	return time.Duration(-l.tokens / float64(l.rate) * float64(time.Second))
}

// limitPiece keeps waits short and the rate smooth.
const limitPiece = 32 << 10

type limitWriter struct {
	l      *limiter
	w      io.Writer
	closed <-chan struct{}
}

func (lw limitWriter) Write(p []byte) (int, error) {
	written := 0
	for len(p) > 0 {
		n := min(len(p), limitPiece)
		if d := lw.l.reserve(n); d > 0 {
			t := time.NewTimer(d)
			select {
			case <-t.C:
			case <-lw.closed:
				t.Stop()
				return written, net.ErrClosed
			}
		}
		k, err := lw.w.Write(p[:n])
		written += k
		if err != nil {
			return written, err
		}
		p = p[n:]
	}
	return written, nil
}

// SetLimit caps the combined send rate of all LAN streams in bytes per
// second (0 = no limit). It applies to open streams at once.
func (t *Transport) SetLimit(bytesPerSec int64) { t.limit.set(bytesPerSec) }

// AddrFromEnv is the listen address: ":<DROPLINK_LAN_PORT>" or ":0".
func AddrFromEnv() string {
	if p, err := strconv.ParseUint(os.Getenv("DROPLINK_LAN_PORT"), 10, 16); err == nil && p > 0 {
		return ":" + strconv.FormatUint(p, 10)
	}
	return ":0"
}

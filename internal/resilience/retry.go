package resilience

import (
	"context"
	"errors"
	"io"
	"math/rand/v2"
	"net"
	"os"
	"time"

	"github.com/vinayakkam/droplink/internal/contract"
	"github.com/vinayakkam/droplink/internal/engine"
)

// Policy is a jittered exponential backoff: Base, 2×Base, ... capped at
// Max, each ±Jitter, for at most Tries attempts in all.
type Policy struct {
	Base   time.Duration // 0.5 s
	Max    time.Duration // 8 s
	Tries  int           // 6
	Jitter float64       // 0.2

	sleep func(context.Context, time.Duration) error // tests
}

func (p Policy) withDefaults() Policy {
	if p.Base <= 0 {
		p.Base = 500 * time.Millisecond
	}
	if p.Max <= 0 {
		p.Max = 8 * time.Second
	}
	if p.Tries <= 0 {
		p.Tries = 6
	}
	if p.Jitter <= 0 || p.Jitter >= 1 {
		p.Jitter = 0.2
	}
	if p.sleep == nil {
		p.sleep = sleepCtx
	}
	return p
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Delay is the wait before attempt n+1 (n counts from 1), with jitter.
func (p Policy) Delay(n int) time.Duration {
	p = p.withDefaults()
	d := p.Base << min(n-1, 20)
	if d > p.Max || d <= 0 {
		d = p.Max
	}
	f := 1 + p.Jitter*(2*rand.Float64()-1)
	return time.Duration(float64(d) * f)
}

// Retryable reports whether err is worth another attempt: the network
// failed, not the person or the protocol. A reject, a cancel, a protocol
// violation, a wrong identity or a full disk are final.
func Retryable(err error) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return false
	case errors.Is(err, engine.ErrRejected), errors.Is(err, engine.ErrCanceled),
		errors.Is(err, engine.ErrProtocol), errors.Is(err, engine.ErrRemote):
		return false
	case errors.Is(err, os.ErrPermission), errors.Is(err, os.ErrNotExist):
		return false
	case errors.Is(err, engine.ErrCorrupt):
		return true // the bad part file was dropped; a retry starts clean
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF),
		errors.Is(err, io.ErrClosedPipe), errors.Is(err, net.ErrClosed):
		return true
	}
	var ne net.Error
	if errors.As(err, &ne) {
		return true
	}
	var op *net.OpError
	return errors.As(err, &op)
}

// Do runs fn until it succeeds, fails for good, the tries run out or ctx
// ends. It returns fn's last error.
func (p Policy) Do(ctx context.Context, fn func(ctx context.Context, attempt int) error) error {
	p = p.withDefaults()
	var err error
	for n := 1; n <= p.Tries; n++ {
		if err = fn(ctx, n); err == nil || !Retryable(err) || n == p.Tries {
			return err
		}
		if serr := p.sleep(ctx, p.Delay(n)); serr != nil {
			return err
		}
	}
	return err
}

// Send sends o to a peer through r, redialing on network failures. Each
// attempt is a fresh stream; the receiver's partial files make it resume
// rather than restart. progress also receives the attempts' updates.
func Send(ctx context.Context, p Policy, r contract.Router, e contract.Engine, to contract.Peer,
	o contract.Offer, src contract.Source, progress func(contract.Progress)) error {
	return p.Do(ctx, func(ctx context.Context, _ int) error {
		s, err := r.Dial(ctx, to)
		if err != nil {
			return err
		}
		return e.Send(ctx, s, o, src, progress)
	})
}

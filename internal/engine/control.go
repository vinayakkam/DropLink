package engine

import (
	"context"
	"sync"
	"time"

	"github.com/vinayakkam/droplink/internal/contract"
)

// Control pauses and resumes one transfer from outside the engine (the
// transfer manager holds it). Pass it with WithControl. Pausing either end
// stops the data; both ends report StatePaused.
type Control struct {
	mu      sync.Mutex
	paused  bool
	changed chan struct{} // closed and replaced on every change
}

// NewControl returns a running (not paused) Control.
func NewControl() *Control { return &Control{changed: make(chan struct{})} }

func (c *Control) set(p bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.paused == p {
		return
	}
	c.paused = p
	close(c.changed)
	c.changed = make(chan struct{})
}

// Pause stops the transfer after the chunk in flight.
func (c *Control) Pause() { c.set(true) }

// Resume continues a paused transfer.
func (c *Control) Resume() { c.set(false) }

// Paused reports the current state.
func (c *Control) Paused() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.paused
}

// watch returns the state and a channel closed on the next change.
func (c *Control) watch() (bool, <-chan struct{}) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.paused, c.changed
}

type controlKey struct{}

// WithControl attaches c to ctx for one Send or Receive call.
func WithControl(ctx context.Context, c *Control) context.Context {
	return context.WithValue(ctx, controlKey{}, c)
}

func controlFrom(ctx context.Context) *Control {
	if c, ok := ctx.Value(controlKey{}).(*Control); ok && c != nil {
		return c
	}
	return NewControl()
}

// reporter rate-limits progress callbacks and smooths the speed.
type reporter struct {
	fn    func(contract.Progress)
	id    contract.TransferID
	total int64
	every time.Duration
	now   func() time.Time

	last     time.Time
	lastDone int64
	rate     float64 // bytes per second, EWMA
}

// progressEvery caps callbacks at 5 per second per transfer.
const progressEvery = 200 * time.Millisecond

func newReporter(fn func(contract.Progress), id contract.TransferID, total int64) *reporter {
	return &reporter{fn: fn, id: id, total: total, every: progressEvery, now: time.Now}
}

// emit reports progress. Unforced calls are dropped inside the interval;
// state changes and the final update are always forced.
func (r *reporter) emit(state contract.TransferState, file int, done int64, err error, force bool) {
	if r.fn == nil {
		return
	}
	now := r.now()
	if r.last.IsZero() {
		r.last, r.lastDone = now, done
	}
	dt := now.Sub(r.last)
	if !force && dt < r.every {
		return
	}
	if dt > 0 {
		inst := float64(done-r.lastDone) / dt.Seconds()
		if r.rate == 0 {
			r.rate = inst
		} else {
			r.rate = 0.3*inst + 0.7*r.rate
		}
		r.last, r.lastDone = now, done
	}
	if state != contract.StateActive {
		r.rate = 0
	}
	p := contract.Progress{ID: r.id, State: state, File: file, Done: done, Total: r.total, BytesPerSec: r.rate}
	if err != nil {
		p.Err = err.Error()
	}
	r.fn(p)
}

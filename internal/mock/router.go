package mock

import (
	"context"
	"errors"
	"slices"
	"sync"

	"github.com/vinayakkam/droplink/internal/contract"
)

// Router tries each transport in order and returns the first stream that
// opens. No retries: p2/resilience adds those.
type Router struct {
	Transports []contract.Transport
}

func (r *Router) Dial(ctx context.Context, to contract.Peer) (contract.Stream, error) {
	var errs []error
	for _, t := range r.Transports {
		p := to
		p.Route = t.Route()
		s, err := t.Dial(ctx, p)
		if err == nil {
			return s, nil
		}
		errs = append(errs, err)
	}
	if len(errs) == 0 {
		return nil, errors.New("mock: router has no transports")
	}
	return nil, errors.Join(errs...)
}

// History keeps records in memory, capped at Max (default 500).
type History struct {
	Max  int
	mu   sync.Mutex
	recs []contract.Record
}

func (h *History) Append(r contract.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	max := h.Max
	if max <= 0 {
		max = 500
	}
	h.recs = append(h.recs, r)
	if len(h.recs) > max {
		h.recs = slices.Delete(h.recs, 0, len(h.recs)-max)
	}
	return nil
}

func (h *History) List(limit int) ([]contract.Record, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := slices.Clone(h.recs)
	slices.Reverse(out)
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (h *History) Clear() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.recs = nil
	return nil
}

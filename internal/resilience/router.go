// Package resilience keeps transfers going: it picks the best route to a
// peer, retries what failed for network reasons, and notices when the
// network changes.
package resilience

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/vinayakkam/droplink/internal/contract"
)

// Router implements contract.Router over the LAN and WebRTC transports.
//
// Order: the route that last worked for this peer first, then LAN if
// discovery heard from the peer within Fresh, then WebRTC (which itself
// falls back to a relay). A route that is not possible is skipped.
type Router struct {
	disc  contract.Discovery
	lan   contract.Transport
	rtc   contract.Transport
	fresh time.Duration
	now   func() time.Time

	mu   sync.Mutex
	last map[contract.DeviceID]contract.Route

	watch *Watcher
}

var _ contract.Router = (*Router)(nil)

// NewRouter returns a Router. Any transport may be nil (route disabled).
func NewRouter(disc contract.Discovery, lan, rtc contract.Transport) *Router {
	return &Router{disc: disc, lan: lan, rtc: rtc, fresh: 7 * time.Second, now: time.Now,
		last: map[contract.DeviceID]contract.Route{}}
}

// ErrNoRoute means no transport can reach the peer right now.
var ErrNoRoute = errors.New("resilience: no route to peer")

// onLAN returns the peer as discovery last saw it, if recently.
func (r *Router) onLAN(id contract.DeviceID) (contract.Peer, bool) {
	if r.disc == nil || r.lan == nil {
		return contract.Peer{}, false
	}
	cutoff := r.now().Add(-r.fresh)
	for _, p := range r.disc.Peers() {
		if p.ID == id && p.Addr != "" && !p.LastSeen.Before(cutoff) {
			return p, true
		}
	}
	return contract.Peer{}, false
}

type route struct {
	t    contract.Transport
	peer contract.Peer
}

// plan lists the routes to try, in order.
func (r *Router) plan(to contract.Peer) []route {
	var routes []route
	if p, ok := r.onLAN(to.ID); ok {
		p.Name = firstNonEmpty(to.Name, p.Name)
		p.Route = contract.RouteLAN
		routes = append(routes, route{r.lan, p})
	}
	if r.rtc != nil {
		p := to
		p.Route, p.Addr = contract.RouteWebRTC, ""
		routes = append(routes, route{r.rtc, p})
	}
	r.mu.Lock()
	pref, ok := r.last[to.ID]
	r.mu.Unlock()
	if ok && len(routes) == 2 && routes[1].t.Route() == pref {
		routes[0], routes[1] = routes[1], routes[0]
	}
	return routes
}

// Dial opens a stream to the peer on the first route that works.
func (r *Router) Dial(ctx context.Context, to contract.Peer) (contract.Stream, error) {
	routes := r.plan(to)
	if len(routes) == 0 {
		return nil, fmt.Errorf("%w %s", ErrNoRoute, to.ID)
	}
	var errs []error
	for _, rt := range routes {
		s, err := rt.t.Dial(ctx, rt.peer)
		if err == nil {
			r.mu.Lock()
			r.last[to.ID] = rt.t.Route()
			r.mu.Unlock()
			return s, nil
		}
		errs = append(errs, fmt.Errorf("%s: %w", rt.t.Route(), err))
		if ctx.Err() != nil {
			break
		}
	}
	return nil, errors.Join(errs...)
}

// Reset forgets which route worked, e.g. after the network changed.
func (r *Router) Reset() {
	r.mu.Lock()
	clear(r.last)
	r.mu.Unlock()
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

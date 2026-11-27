package resilience

import (
	"net"
	"sort"
	"strings"
	"sync"
	"time"
)

// Watcher polls the network interfaces and calls OnChange when the set of
// up interfaces or their addresses changes (Wi-Fi switch, VPN, cable).
type Watcher struct {
	Every    time.Duration // 5 s
	OnChange func()

	fingerprint func() string // tests fake network changes
	stop        chan struct{}
	done        chan struct{}
	once        sync.Once
}

// Start begins polling in one goroutine; Close stops it.
func (w *Watcher) Start() {
	if w.Every <= 0 {
		w.Every = 5 * time.Second
	}
	if w.fingerprint == nil {
		w.fingerprint = Fingerprint
	}
	w.stop, w.done = make(chan struct{}), make(chan struct{})
	go w.loop()
}

func (w *Watcher) loop() {
	defer close(w.done)
	t := time.NewTicker(w.Every)
	defer t.Stop()
	last := w.fingerprint()
	for {
		select {
		case <-w.stop:
			return
		case <-t.C:
			if now := w.fingerprint(); now != last {
				last = now
				if w.OnChange != nil {
					w.OnChange()
				}
			}
		}
	}
}

// Close stops the watcher and waits for its goroutine. Safe to call
// more than once, or without Start.
func (w *Watcher) Close() error {
	w.once.Do(func() {
		if w.stop != nil {
			close(w.stop)
			<-w.done
		}
	})
	return nil
}

// Fingerprint describes the up interfaces and their addresses, in a
// stable order.
func Fingerprint() string {
	ifs, err := net.Interfaces()
	if err != nil {
		return ""
	}
	var parts []string
	for _, ifi := range ifs {
		if ifi.Flags&net.FlagUp == 0 {
			continue
		}
		addrs, _ := ifi.Addrs()
		as := make([]string, 0, len(addrs))
		for _, a := range addrs {
			as = append(as, a.String())
		}
		sort.Strings(as)
		parts = append(parts, ifi.Name+"="+strings.Join(as, ","))
	}
	sort.Strings(parts)
	return strings.Join(parts, ";")
}

// WatchNetwork makes the router forget its remembered routes whenever the
// network changes, then calls also (for example: announce on the LAN at
// once). Close stops watching.
func (r *Router) WatchNetwork(every time.Duration, also func()) {
	r.watch = &Watcher{Every: every, OnChange: func() {
		r.Reset()
		if also != nil {
			also()
		}
	}}
	r.watch.Start()
}

// Close stops the network watcher, if any.
func (r *Router) Close() error {
	if r.watch == nil {
		return nil
	}
	return r.watch.Close()
}

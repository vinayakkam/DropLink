// Package app wires the sidecar together.
//
// providers.go is the one file both people touch, and only in agreed spots:
// every P2 branch replaces exactly one marked line below with its real
// implementation (see docs/TASKS.md, "Contracts"). Nothing else changes, so
// the UI and the routes keep working at every step.
package app

import (
	"errors"
	"io"
	"path/filepath"

	"github.com/vinayakkam/droplink/internal/config"
	"github.com/vinayakkam/droplink/internal/contract"
	dlcrypto "github.com/vinayakkam/droplink/internal/crypto"
	"github.com/vinayakkam/droplink/internal/discovery"
	"github.com/vinayakkam/droplink/internal/history"
	"github.com/vinayakkam/droplink/internal/lan"
	"github.com/vinayakkam/droplink/internal/resilience"
	"github.com/vinayakkam/droplink/internal/signal"
	"github.com/vinayakkam/droplink/internal/webrtc"
)

// Providers holds one implementation of each contract interface.
type Providers struct {
	Self      contract.Peer
	Crypto    contract.Crypto
	Discovery contract.Discovery
	LAN       contract.Transport
	WebRTC    contract.Transport
	Signaler  contract.Signaler
	Router    contract.Router
	History   contract.History
}

// NewProviders builds the implementations for this run.
func NewProviders(cfg config.Config) (*Providers, error) {
	// p2/crypto: identity key and trust list in cfg.DataDir.
	crypto, err := dlcrypto.Load(cfg.DataDir)
	if err != nil {
		return nil, err
	}

	self := contract.Peer{ID: crypto.ID(), Name: cfg.DeviceName, Route: contract.RouteLAN}
	// p2/lan-discovery: UDP beacons on the LAN.
	disc := discovery.New(self, discovery.PortFromEnv(), discovery.Options{})
	// p2/lan-transfer: encrypted TCP; its port goes into the beacons.
	lanT, err := lan.New(self, crypto, lan.Options{Addr: lan.AddrFromEnv(), OnPort: disc.SetPort})
	if err != nil {
		return nil, err
	}
	// p2/signaling: WebSocket hub at DROPLINK_SIGNAL_URL.
	sig := signal.New(signal.URLFromEnv())
	// p2/webrtc: DataChannels to room members, ICE from DROPLINK_ICE.
	ice, err := webrtc.ICEFromEnv()
	if err != nil {
		lanT.Close()
		return nil, err
	}
	rtc := webrtc.New(self, sig, webrtc.Options{ICEServers: ice})

	p := &Providers{
		Self:      self,
		Crypto:    crypto,
		Discovery: disc,
		LAN:       lanT,
		WebRTC:    rtc,
		Signaler:  sig,
		// p2/resilience: opt-in log in cfg.DataDir (off until settings enable it).
		History: history.New(filepath.Join(cfg.DataDir, "history.jsonl"), history.DefaultMax),
	}
	// p2/resilience: LAN when seen, else WebRTC; re-plan after network changes.
	router := resilience.NewRouter(disc, lanT, rtc)
	router.WatchNetwork(0, func() { disc.SetPort(lanT.Port()) }) // announce at once
	p.Router = router
	return p, nil
}

// Close releases the transports.
func (p *Providers) Close() error {
	var errs []error
	if c, ok := p.Router.(io.Closer); ok {
		errs = append(errs, c.Close())
	}
	return errors.Join(append(errs, p.LAN.Close(), p.WebRTC.Close())...)
}

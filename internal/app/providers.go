// Package app wires the sidecar together.
//
// providers.go is the one file both people touch, and only in agreed spots:
// every P2 branch replaces exactly one marked line below with its real
// implementation (see docs/TASKS.md, "Contracts"). Nothing else changes, so
// the UI and the routes keep working at every step.
package app

import (
	"errors"

	"github.com/vinayakkam/droplink/internal/config"
	"github.com/vinayakkam/droplink/internal/contract"
	dlcrypto "github.com/vinayakkam/droplink/internal/crypto"
	"github.com/vinayakkam/droplink/internal/discovery"
	"github.com/vinayakkam/droplink/internal/mock"
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
	network := mock.NewNetwork()
	webrtcSelf := self
	webrtcSelf.Route = contract.RouteWebRTC

	p := &Providers{
		Self:   self,
		Crypto: crypto,
		// p2/lan-discovery: UDP beacons on the LAN.
		Discovery: discovery.New(self, discovery.PortFromEnv(), discovery.Options{}),
		// p2/lan-transfer replaces this line.
		LAN: network.Join(self, contract.RouteLAN),
		// p2/webrtc replaces this line.
		WebRTC: network.Join(webrtcSelf, contract.RouteWebRTC),
		// p2/signaling replaces this line.
		Signaler: mock.NewHub(),
		// p2/resilience replaces this line (history file in cfg.DataDir).
		History: &mock.History{},
	}
	// p2/resilience replaces this line.
	p.Router = &mock.Router{Transports: []contract.Transport{p.LAN, p.WebRTC}}
	return p, nil
}

// Close releases the transports.
func (p *Providers) Close() error {
	return errors.Join(p.LAN.Close(), p.WebRTC.Close())
}

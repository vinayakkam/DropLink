// Package app wires the sidecar together.
//
// providers.go is the one file both people touch, and only in agreed spots:
// every P2 branch replaces exactly one marked line below with its real
// implementation (see docs/TASKS.md, "Contracts"). Nothing else changes, so
// the UI and the routes keep working at every step.
package app

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"

	"github.com/vinayakkam/droplink/internal/config"
	"github.com/vinayakkam/droplink/internal/contract"
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
	// p2/crypto replaces this line (load or create the identity key in cfg.DataDir).
	var crypto contract.Crypto = mock.NewCrypto(placeholderID(cfg))

	self := contract.Peer{ID: crypto.ID(), Name: cfg.DeviceName, Route: contract.RouteLAN}
	network := mock.NewNetwork()
	webrtcSelf := self
	webrtcSelf.Route = contract.RouteWebRTC

	p := &Providers{
		Self:   self,
		Crypto: crypto,
		// p2/lan-discovery replaces this line.
		Discovery: mock.NewDiscovery(),
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

// placeholderID is stable per data dir and name until p2/crypto adds keys.
func placeholderID(cfg config.Config) contract.DeviceID {
	sum := sha256.Sum256([]byte(cfg.DataDir + "|" + cfg.DeviceName))
	return contract.DeviceID(hex.EncodeToString(sum[:16]))
}

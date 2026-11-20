package app

import (
	"testing"

	"github.com/vinayakkam/droplink/internal/config"
	"github.com/vinayakkam/droplink/internal/testutil"
)

func TestNewProviders(t *testing.T) {
	testutil.NoLeaks(t)
	cfg := config.Default()
	cfg.DataDir = t.TempDir()
	cfg.DeviceName = "node"

	p, err := NewProviders(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	if len(p.Self.ID) != 32 || p.Self.ID != p.Crypto.ID() || p.Self.Name != "node" {
		t.Fatalf("self = %+v", p.Self)
	}
	for name, v := range map[string]any{
		"Crypto": p.Crypto, "Discovery": p.Discovery, "LAN": p.LAN, "WebRTC": p.WebRTC,
		"Signaler": p.Signaler, "Router": p.Router, "History": p.History,
	} {
		if v == nil {
			t.Errorf("%s is nil", name)
		}
	}
	if p.LAN.Route() != "lan" || p.WebRTC.Route() != "webrtc" {
		t.Fatal("transports on wrong routes")
	}

	again, _ := NewProviders(cfg)
	defer again.Close()
	if again.Self.ID != p.Self.ID {
		t.Fatal("device ID not stable across runs")
	}
}

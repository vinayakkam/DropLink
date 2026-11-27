package main

import (
	"log/slog"
	"path/filepath"

	"github.com/vinayakkam/droplink/internal/api"
	"github.com/vinayakkam/droplink/internal/app"
	"github.com/vinayakkam/droplink/internal/config"
	"github.com/vinayakkam/droplink/internal/contract"
	"github.com/vinayakkam/droplink/internal/manager"
	"github.com/vinayakkam/droplink/internal/server"
)

// wire builds the settings and the transfer manager on top of the
// providers and registers the API routes. Settings apply live: the speed
// limit, the history switch and the radar profile change at once.
func wire(cfg config.Config, p *app.Providers, srv *server.Server, log *slog.Logger) (*manager.Manager, error) {
	settings, err := api.LoadSettings(filepath.Join(cfg.DataDir, "settings.json"), api.DefaultSettings(cfg.DeviceName))
	if err != nil {
		return nil, err
	}
	settings.OnChange(func(s api.Settings) {
		if l, ok := p.LAN.(interface{ SetLimit(int64) }); ok {
			l.SetLimit(int64(s.SpeedLimitKBps) << 10)
		}
		if h, ok := p.History.(interface{ SetEnabled(bool) }); ok {
			h.SetEnabled(s.History)
		}
		if d, ok := p.Discovery.(interface{ SetProfile(string, uint8) }); ok {
			d.SetProfile(s.Name, s.Avatar)
		}
	})

	m := manager.New(manager.Deps{
		Self: p.Self, Crypto: p.Crypto, Discovery: p.Discovery, Router: p.Router, History: p.History,
		Transports: []contract.Transport{p.LAN, p.WebRTC}, Log: log,
	}, manager.Options{
		DownloadDir:       func() string { return settings.Get().DownloadDir },
		AutoAcceptTrusted: func() bool { return settings.Get().AutoAcceptTrusted },
	})

	deps := api.Deps{Manager: m, Crypto: p.Crypto, History: p.History, Settings: settings}
	if r, ok := p.WebRTC.(api.Roomer); ok {
		deps.Rooms = r
	}
	api.Register(srv, deps)
	m.Run()
	return m, nil
}

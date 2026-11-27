// Package api is the sidecar's HTTP API for the UI: peers, transfers,
// rooms, trust, settings, history and a server-sent event stream. Routes
// are registered on internal/server, so they share its token check.
package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unicode/utf8"
)

// Settings are this device's preferences, in <data dir>/settings.json.
type Settings struct {
	Name              string `json:"name"`
	Avatar            uint8  `json:"avatar"`
	DownloadDir       string `json:"downloadDir"`
	AutoAcceptTrusted bool   `json:"autoAcceptTrusted"`
	SpeedLimitKBps    int    `json:"speedLimitKBps"` // 0 = no limit
	Volume            int    `json:"volume"`         // 0..100, 0 = silent
	Theme             string `json:"theme"`          // cyan | amber | magenta
	Motion            bool   `json:"motion"`         // animations on
	History           bool   `json:"history"`        // keep a local log
	CloseToTray       bool   `json:"closeToTray"`
}

// Themes the UI ships.
var Themes = []string{"cyan", "amber", "magenta"}

// DefaultSettings for a device named name.
func DefaultSettings(name string) Settings {
	home, err := os.UserHomeDir()
	if err != nil {
		home = os.TempDir()
	}
	return Settings{
		Name:              name,
		DownloadDir:       filepath.Join(home, "Downloads", "DropLink"),
		AutoAcceptTrusted: true,
		Volume:            40,
		Theme:             "cyan",
		Motion:            true,
	}
}

// Validate rejects values the UI should never send.
func (s Settings) Validate() error {
	switch {
	case strings.TrimSpace(s.Name) == "" || len(s.Name) > 64 || !utf8.ValidString(s.Name):
		return errors.New("name must be 1 to 64 characters")
	case s.Avatar > 15:
		return errors.New("avatar must be 0..15")
	case s.DownloadDir == "" || !filepath.IsAbs(s.DownloadDir):
		return errors.New("download folder must be an absolute path")
	case s.SpeedLimitKBps < 0 || s.SpeedLimitKBps > 10_000_000:
		return errors.New("speed limit out of range")
	case s.Volume < 0 || s.Volume > 100:
		return errors.New("volume must be 0..100")
	}
	for _, t := range Themes {
		if s.Theme == t {
			return nil
		}
	}
	return fmt.Errorf("unknown theme %q", s.Theme)
}

// SettingsStore loads, saves and applies Settings.
type SettingsStore struct {
	path  string
	mu    sync.Mutex
	s     Settings
	apply []func(Settings)
}

// LoadSettings reads path, falling back to defaults for a missing file or
// missing fields. A damaged file is reported, not overwritten.
func LoadSettings(path string, defaults Settings) (*SettingsStore, error) {
	st := &SettingsStore{path: path, s: defaults}
	b, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return st, nil
	case err != nil:
		return nil, err
	}
	s := defaults
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("settings: %s is damaged: %w", path, err)
	}
	if s.Validate() != nil {
		s = defaults // an older or hand-edited file with bad values
	}
	st.s = s
	return st, nil
}

// OnChange registers fn to run with the settings now and after each
// change (speed limit, history switch, radar profile...).
func (st *SettingsStore) OnChange(fn func(Settings)) {
	st.mu.Lock()
	st.apply = append(st.apply, fn)
	s := st.s
	st.mu.Unlock()
	fn(s)
}

// Get returns the current settings.
func (st *SettingsStore) Get() Settings {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.s
}

// Set validates, saves atomically and applies s.
func (st *SettingsStore) Set(s Settings) error {
	s.Name = strings.TrimSpace(s.Name)
	if err := s.Validate(); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(st.path), 0o700); err != nil {
		return err
	}
	tmp := st.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, st.path); err != nil {
		return err
	}
	st.mu.Lock()
	st.s = s
	fns := append([]func(Settings){}, st.apply...)
	st.mu.Unlock()
	for _, fn := range fns {
		fn(s)
	}
	return nil
}

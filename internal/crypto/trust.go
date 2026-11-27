package crypto

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/vinayakkam/droplink/internal/contract"
)

// TrustFile is the trust list inside the data dir.
const TrustFile = "trusted.json"

var _ contract.Crypto = (*Crypto)(nil)

type trustStore struct {
	path string
	mu   sync.Mutex
	devs map[contract.DeviceID]contract.TrustedDevice
}

// Load opens this device's identity and trust list in dataDir.
func Load(dataDir string) (*Crypto, error) {
	id, err := LoadIdentity(dataDir)
	if err != nil {
		return nil, err
	}
	ts := &trustStore{path: filepath.Join(dataDir, TrustFile), devs: map[contract.DeviceID]contract.TrustedDevice{}}
	if err := ts.load(); err != nil {
		return nil, err
	}
	return &Crypto{id: id, trust: ts}, nil
}

func (t *trustStore) load() error {
	b, err := os.ReadFile(t.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("crypto: read trust list: %w", err)
	}
	var list []contract.TrustedDevice
	if err := json.Unmarshal(b, &list); err != nil {
		return fmt.Errorf("crypto: %s is damaged: %w", t.path, err)
	}
	for _, d := range list {
		t.devs[d.ID] = d
	}
	return nil
}

// save must be called with t.mu held.
func (t *trustStore) save() error {
	list := t.sortedLocked()
	b, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(t.path, b, 0o600)
}

func (t *trustStore) sortedLocked() []contract.TrustedDevice {
	list := make([]contract.TrustedDevice, 0, len(t.devs))
	for _, d := range t.devs {
		list = append(list, d)
	}
	slices.SortFunc(list, func(a, b contract.TrustedDevice) int { return strings.Compare(string(a.ID), string(b.ID)) })
	return list
}

func validID(id contract.DeviceID) bool {
	if len(id) != 32 {
		return false
	}
	for _, r := range id {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}

// Trust adds or renames a device in the trust list.
func (c *Crypto) Trust(d contract.TrustedDevice) error {
	if !validID(d.ID) {
		return fmt.Errorf("crypto: %q is not a device ID", d.ID)
	}
	if d.Added.IsZero() {
		d.Added = time.Now().UTC()
	}
	if len(d.Name) > 64 {
		d.Name = d.Name[:64]
	}
	t := c.trust
	t.mu.Lock()
	defer t.mu.Unlock()
	if old, ok := t.devs[d.ID]; ok {
		d.Added = old.Added // keep the first date
	}
	t.devs[d.ID] = d
	return t.save()
}

// Untrust removes a device. Removing an unknown device is not an error.
func (c *Crypto) Untrust(id contract.DeviceID) error {
	t := c.trust
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, ok := t.devs[id]; !ok {
		return nil
	}
	delete(t.devs, id)
	return t.save()
}

// Trusted lists trusted devices sorted by ID.
func (c *Crypto) Trusted() []contract.TrustedDevice {
	t := c.trust
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.sortedLocked()
}

// IsTrusted reports whether id is in the trust list.
func (c *Crypto) IsTrusted(id contract.DeviceID) bool {
	t := c.trust
	t.mu.Lock()
	defer t.mu.Unlock()
	_, ok := t.devs[id]
	return ok
}

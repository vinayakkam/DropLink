// Package history implements contract.History: an opt-in log of finished
// transfers in one JSON-lines file. It records names, sizes, peers and
// outcomes only, never file contents or clipboard text.
package history

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"

	"github.com/vinayakkam/droplink/internal/contract"
)

// DefaultMax is how many records are kept.
const DefaultMax = 500

// maxLine bounds one record (a transfer of a few thousand files).
const maxLine = 4 << 20

// Store is a capped JSONL file, newest record last.
type Store struct {
	path string
	max  int

	mu      sync.Mutex
	enabled bool
	count   int // records in the file, -1 = not counted yet
}

var _ contract.History = (*Store)(nil)

// New returns a Store at path keeping up to max records (DefaultMax when
// max <= 0). It starts disabled: history is opt-in.
func New(path string, max int) *Store {
	if max <= 0 {
		max = DefaultMax
	}
	return &Store{path: path, max: max, count: -1}
}

// SetEnabled turns recording on or off. Turning it off keeps what is
// there; Clear deletes it.
func (s *Store) SetEnabled(on bool) {
	s.mu.Lock()
	s.enabled = on
	s.mu.Unlock()
}

// Enabled reports whether Append records anything.
func (s *Store) Enabled() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.enabled
}

// Append adds r when history is on. Text shares keep only the fact that
// text was sent. Past the cap, the oldest records are dropped.
func (s *Store) Append(r contract.Record) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.enabled {
		return nil
	}
	line, err := json.Marshal(r)
	if err != nil {
		return err
	}
	if len(line) > maxLine {
		return fmt.Errorf("history: record of %d bytes is too large", len(line))
	}
	if s.count < 0 {
		recs, err := s.readLocked()
		if err != nil {
			return err
		}
		s.count = len(recs)
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(s.path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	_, werr := f.Write(append(line, '\n'))
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		return werr
	}
	s.count++
	if s.count > s.max {
		return s.compactLocked()
	}
	return nil
}

// List returns up to limit records, newest first (limit <= 0: all).
// Damaged lines are skipped, so a crash mid-write loses one record at
// most.
func (s *Store) List(limit int) ([]contract.Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	recs, err := s.readLocked()
	if err != nil {
		return nil, err
	}
	slices.Reverse(recs)
	if limit > 0 && len(recs) > limit {
		recs = recs[:limit]
	}
	return recs, nil
}

// Clear deletes the history file.
func (s *Store) Clear() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.count = 0
	if err := os.Remove(s.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// readLocked returns the records oldest first.
func (s *Store) readLocked() ([]contract.Record, error) {
	f, err := os.Open(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var recs []contract.Record
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64<<10), maxLine+1)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var r contract.Record
		if json.Unmarshal(line, &r) != nil {
			continue // damaged line
		}
		recs = append(recs, r)
	}
	if err := sc.Err(); err != nil && !errors.Is(err, bufio.ErrTooLong) {
		return nil, err
	}
	return recs, nil
}

// compactLocked rewrites the file with the newest max records, through a
// temp file and rename so a crash never truncates history.
func (s *Store) compactLocked() error {
	recs, err := s.readLocked()
	if err != nil {
		return err
	}
	if len(recs) > s.max {
		recs = recs[len(recs)-s.max:]
	}
	var buf bytes.Buffer
	for _, r := range recs {
		line, _ := json.Marshal(r)
		buf.Write(line)
		buf.WriteByte('\n')
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".history-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(buf.Bytes()); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, s.path); err != nil {
		return err
	}
	s.count = len(recs)
	return nil
}

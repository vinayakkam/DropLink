package files

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/vinayakkam/droplink/internal/contract"
	"github.com/vinayakkam/droplink/internal/engine"
)

// PartSuffix marks a file still being received. A later transfer of the
// same file continues from its end.
const PartSuffix = ".droplink-part"

// Sink stores received files under Dir.
//
//   - Data goes to "<name>.droplink-part" and is renamed on Commit, so a
//     half-received file never looks finished.
//   - An interrupted transfer leaves the part file; the next transfer of
//     the same path resumes after its last byte.
//   - A finished file never overwrites an existing one: it becomes
//     "name (2).ext".
//   - Paths are re-checked here too; nothing lands outside Dir.
type Sink struct {
	Dir string

	mu    sync.Mutex
	saved map[string]string // offered path -> final path, after Commit
}

var _ contract.Sink = (*Sink)(nil)

// NewSink returns a Sink rooted at dir (created if missing).
func NewSink(dir string) (*Sink, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return nil, err
	}
	return &Sink{Dir: abs, saved: map[string]string{}}, nil
}

func (s *Sink) target(f contract.FileMeta) (final, part string, err error) {
	rel, err := engine.CleanPath(f.Path)
	if err != nil {
		return "", "", err
	}
	final = filepath.Join(s.Dir, filepath.FromSlash(rel))
	if !strings.HasPrefix(final, s.Dir+string(filepath.Separator)) {
		return "", "", fmt.Errorf("files: %q escapes the download folder", f.Path)
	}
	return final, final + PartSuffix, nil
}

// Have reports how many bytes of f a previous transfer already stored.
// The engine uses it to ask the sender to skip them.
func (s *Sink) Have(f contract.FileMeta) int64 {
	_, part, err := s.target(f)
	if err != nil {
		return 0
	}
	info, err := os.Stat(part)
	if err != nil || !info.Mode().IsRegular() || info.Size() > f.Size {
		return 0
	}
	return info.Size()
}

// Open returns the part file positioned at its end. A part file longer
// than f is stale and starts over.
func (s *Sink) Open(f contract.FileMeta) (io.WriteCloser, int64, error) {
	_, part, err := s.target(f)
	if err != nil {
		return nil, 0, err
	}
	if err := os.MkdirAll(filepath.Dir(part), 0o755); err != nil {
		return nil, 0, err
	}
	fh, err := os.OpenFile(part, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, 0, err
	}
	have, err := fh.Seek(0, io.SeekEnd)
	if err == nil && have > f.Size {
		if err = fh.Truncate(0); err == nil {
			have, err = fh.Seek(0, io.SeekStart)
		}
	}
	if err != nil {
		fh.Close()
		return nil, 0, err
	}
	return fh, have, nil
}

// Commit moves the verified part file to its final name.
func (s *Sink) Commit(f contract.FileMeta) error {
	final, part, err := s.target(f)
	if err != nil {
		return err
	}
	dest := freeName(final)
	if err := os.Rename(part, dest); err != nil {
		return err
	}
	if !f.ModTime.IsZero() {
		_ = os.Chtimes(dest, f.ModTime, f.ModTime)
	}
	s.mu.Lock()
	s.saved[f.Path] = dest
	s.mu.Unlock()
	return nil
}

// Abort deletes a part file whose contents failed verification, so the
// next attempt starts clean instead of resuming corrupt bytes.
func (s *Sink) Abort(f contract.FileMeta, _ error) error {
	_, part, err := s.target(f)
	if err != nil {
		return err
	}
	if err := os.Remove(part); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// Saved returns where a committed file ended up.
func (s *Sink) Saved(offered string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.saved[offered]
	return p, ok
}

// freeName returns p, or "p (2).ext", "p (3).ext"... if p exists.
func freeName(p string) string {
	if _, err := os.Lstat(p); errors.Is(err, os.ErrNotExist) {
		return p
	}
	ext := filepath.Ext(p)
	base := strings.TrimSuffix(p, ext)
	for i := 2; ; i++ {
		c := base + " (" + strconv.Itoa(i) + ")" + ext
		if _, err := os.Lstat(c); errors.Is(err, os.ErrNotExist) {
			return c
		}
	}
}

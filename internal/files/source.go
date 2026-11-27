// Package files connects the transfer engine to the disk: a Source that
// turns picked files and folders into an offer, and a Sink that stores
// received files safely and resumably.
package files

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/vinayakkam/droplink/internal/contract"
	"github.com/vinayakkam/droplink/internal/engine"
)

// ErrTooMany is returned when a selection has more files than one offer
// may carry.
var ErrTooMany = errors.New("files: too many files in one transfer")

// MaxFiles matches the engine's offer limit.
const MaxFiles = 100_000

// Source serves the files of one offer from disk. It maps each offered
// relative path back to the absolute path the user picked.
type Source struct {
	abs map[string]string
}

var _ contract.Source = (*Source)(nil)

// Walk turns picked paths into offer entries and a Source for them.
// A picked file becomes its base name; a picked folder keeps its name as
// the top directory, so "Photos/2026/a.jpg" arrives as such. Symlinks
// are skipped (they could point anywhere), and so are empty folders.
func Walk(picked []string) ([]contract.FileMeta, *Source, error) {
	src := &Source{abs: map[string]string{}}
	var metas []contract.FileMeta
	taken := map[string]bool{}

	add := func(rel, abs string, info fs.FileInfo) error {
		if len(metas) >= MaxFiles {
			return ErrTooMany
		}
		rel = uniqueRel(rel, taken)
		if _, err := engine.CleanPath(rel); err != nil {
			return fmt.Errorf("files: cannot send %q: %w", abs, err)
		}
		taken[strings.ToLower(rel)] = true
		src.abs[rel] = abs
		metas = append(metas, contract.FileMeta{Path: rel, Size: info.Size(), ModTime: info.ModTime()})
		return nil
	}

	for _, p := range picked {
		abs, err := filepath.Abs(p)
		if err != nil {
			return nil, nil, err
		}
		info, err := os.Lstat(abs)
		if err != nil {
			return nil, nil, err
		}
		switch {
		case info.Mode().IsRegular():
			if err := add(filepath.Base(abs), abs, info); err != nil {
				return nil, nil, err
			}
		case info.IsDir():
			root := filepath.Base(abs)
			err := filepath.WalkDir(abs, func(fp string, d fs.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if !d.Type().IsRegular() {
					return nil // dirs are implied by paths; symlinks and devices skipped
				}
				info, err := d.Info()
				if err != nil {
					return err
				}
				rel, err := filepath.Rel(abs, fp)
				if err != nil {
					return err
				}
				return add(path.Join(root, filepath.ToSlash(rel)), fp, info)
			})
			if err != nil {
				return nil, nil, err
			}
		}
	}
	return metas, src, nil
}

// uniqueRel renames a clash between two picked items ("a.txt" twice
// from different folders) to "a (2).txt".
func uniqueRel(rel string, taken map[string]bool) string {
	if !taken[strings.ToLower(rel)] {
		return rel
	}
	ext := path.Ext(rel)
	base := strings.TrimSuffix(rel, ext)
	for i := 2; ; i++ {
		c := base + " (" + strconv.Itoa(i) + ")" + ext
		if !taken[strings.ToLower(c)] {
			return c
		}
	}
}

// Open opens an offered file.
func (s *Source) Open(f contract.FileMeta) (io.ReadSeekCloser, error) {
	abs, ok := s.abs[f.Path]
	if !ok {
		return nil, fmt.Errorf("files: %q was not offered", f.Path)
	}
	return os.Open(abs)
}

// Text is a Source for clipboard shares, which carry no files.
type Text struct{}

func (Text) Open(f contract.FileMeta) (io.ReadSeekCloser, error) {
	return nil, fmt.Errorf("files: text share has no file %q", f.Path)
}

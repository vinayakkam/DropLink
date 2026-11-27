package files

import (
	"io"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/vinayakkam/droplink/internal/contract"
)

func write(t *testing.T, p, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestWalkFilesAndFolders(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "Photos", "2026", "a.jpg"), "aa")
	write(t, filepath.Join(root, "Photos", "b.jpg"), "b")
	write(t, filepath.Join(root, "notes.txt"), "n")
	write(t, filepath.Join(root, "other", "notes.txt"), "clash")
	_ = os.MkdirAll(filepath.Join(root, "Photos", "empty"), 0o755)

	metas, src, err := Walk([]string{
		filepath.Join(root, "Photos"),
		filepath.Join(root, "notes.txt"),
		filepath.Join(root, "other", "notes.txt"),
	})
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, m := range metas {
		paths = append(paths, m.Path)
	}
	want := []string{"Photos/2026/a.jpg", "Photos/b.jpg", "notes.txt", "notes (2).txt"}
	if !slices.Equal(paths, want) {
		t.Fatalf("paths = %v, want %v", paths, want)
	}
	if metas[0].Size != 2 || metas[0].ModTime.IsZero() {
		t.Fatalf("meta = %+v", metas[0])
	}

	r, err := src.Open(metas[3])
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(r)
	r.Close()
	if string(b) != "clash" {
		t.Fatalf("opened %q", b)
	}
	if _, err := src.Open(contract.FileMeta{Path: "not/offered"}); err == nil {
		t.Fatal("source opened a path it never offered")
	}
}

func TestWalkSkipsSymlinks(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "d", "real.txt"), "r")
	if err := os.Symlink(filepath.Join(root, "d", "real.txt"), filepath.Join(root, "d", "link.txt")); err != nil {
		t.Skip("symlinks need privileges here:", err)
	}
	metas, _, err := Walk([]string{filepath.Join(root, "d")})
	if err != nil {
		t.Fatal(err)
	}
	if len(metas) != 1 || metas[0].Path != "d/real.txt" {
		t.Fatalf("metas = %+v", metas)
	}
}

func TestWalkMissingPath(t *testing.T) {
	if _, _, err := Walk([]string{filepath.Join(t.TempDir(), "nope")}); err == nil {
		t.Fatal("no error for a missing path")
	}
}

func TestSinkLifecycle(t *testing.T) {
	dir := t.TempDir()
	s, err := NewSink(dir)
	if err != nil {
		t.Fatal(err)
	}
	mtime := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	f := contract.FileMeta{Path: "sub/x.txt", Size: 6, ModTime: mtime}

	if s.Have(f) != 0 {
		t.Fatal("Have before anything was stored")
	}
	w, have, err := s.Open(f)
	if err != nil || have != 0 {
		t.Fatal(have, err)
	}
	_, _ = w.Write([]byte("abc"))
	w.Close()
	if s.Have(f) != 3 {
		t.Fatalf("Have = %d after 3 bytes", s.Have(f))
	}

	w, have, _ = s.Open(f) // resume
	if have != 3 {
		t.Fatalf("resume have = %d", have)
	}
	if _, ok := w.(io.ReaderAt); !ok {
		t.Fatal("sink writer must be readable back for resume hashing")
	}
	_, _ = w.Write([]byte("def"))
	w.Close()
	if err := s.Commit(f); err != nil {
		t.Fatal(err)
	}
	final := filepath.Join(dir, "sub", "x.txt")
	if b, _ := os.ReadFile(final); string(b) != "abcdef" {
		t.Fatalf("final = %q", b)
	}
	if info, _ := os.Stat(final); !info.ModTime().Equal(mtime) {
		t.Fatalf("mtime = %v", info.ModTime())
	}
	if p, ok := s.Saved("sub/x.txt"); !ok || p != final {
		t.Fatalf("Saved = %q %v", p, ok)
	}

	// A second file with the same name never overwrites.
	w, _, _ = s.Open(f)
	_, _ = w.Write([]byte("second"))
	w.Close()
	_ = s.Commit(f)
	if b, _ := os.ReadFile(filepath.Join(dir, "sub", "x (2).txt")); string(b) != "second" {
		t.Fatalf("clash copy = %q", b)
	}
	if b, _ := os.ReadFile(final); string(b) != "abcdef" {
		t.Fatal("existing file was overwritten")
	}
}

func TestSinkStalePartStartsOver(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewSink(dir)
	f := contract.FileMeta{Path: "y", Size: 2}
	write(t, filepath.Join(dir, "y"+PartSuffix), "much too long")
	if s.Have(f) != 0 {
		t.Fatal("stale part counted as progress")
	}
	w, have, err := s.Open(f)
	if err != nil || have != 0 {
		t.Fatal(have, err)
	}
	w.Close()
}

func TestSinkAbortRemovesPart(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewSink(dir)
	f := contract.FileMeta{Path: "z", Size: 9}
	w, _, _ := s.Open(f)
	w.Close()
	if err := s.Abort(f, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "z"+PartSuffix)); !os.IsNotExist(err) {
		t.Fatal("part survived Abort")
	}
	if err := s.Abort(f, nil); err != nil {
		t.Fatalf("second Abort = %v", err)
	}
}

func TestSinkRejectsEscapes(t *testing.T) {
	s, _ := NewSink(t.TempDir())
	for _, p := range []string{"../x", "/abs", "a/../../b", `..\x`, "c:/x"} {
		if _, _, err := s.Open(contract.FileMeta{Path: p, Size: 1}); err == nil {
			t.Errorf("Open(%q) succeeded", p)
		}
		if err := s.Commit(contract.FileMeta{Path: p}); err == nil {
			t.Errorf("Commit(%q) succeeded", p)
		}
	}
}

package history

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vinayakkam/droplink/internal/contract"
)

func rec(id string) contract.Record {
	return contract.Record{ID: contract.TransferID(id), Peer: contract.Peer{ID: "p", Name: "Vega"},
		Files: []contract.FileMeta{{Path: "a.txt", Size: 3}}, State: contract.StateDone, Bytes: 3,
		StartedAt: time.Unix(1, 0).UTC(), EndedAt: time.Unix(2, 0).UTC()}
}

func ids(rs []contract.Record) string {
	var b []string
	for _, r := range rs {
		b = append(b, string(r.ID))
	}
	return strings.Join(b, ",")
}

func TestOptIn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "h.jsonl")
	s := New(path, 0)
	if s.Enabled() {
		t.Fatal("history on by default")
	}
	_ = s.Append(rec("1"))
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("wrote while disabled")
	}
	s.SetEnabled(true)
	_ = s.Append(rec("2"))
	s.SetEnabled(false)
	_ = s.Append(rec("3"))
	if got, _ := s.List(0); ids(got) != "2" {
		t.Fatalf("records %s", ids(got))
	}
}

func TestOrderLimitAndCap(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "h.jsonl")
	s := New(path, 3)
	s.SetEnabled(true)
	for _, id := range []string{"1", "2", "3", "4", "5"} {
		if err := s.Append(rec(id)); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := s.List(0)
	if ids(got) != "5,4,3" {
		t.Fatalf("records %s, want newest 3 newest first", ids(got))
	}
	if got, _ := s.List(2); ids(got) != "5,4" {
		t.Fatalf("limit: %s", ids(got))
	}
	b, _ := os.ReadFile(path)
	if n := strings.Count(string(b), "\n"); n != 3 {
		t.Fatalf("file has %d lines after the cap", n)
	}
	if got[0].Peer.Name != "Vega" || got[0].Files[0].Path != "a.txt" {
		t.Fatalf("round trip lost fields: %+v", got[0])
	}

	// A new Store on the same file counts what is there.
	again := New(path, 3)
	again.SetEnabled(true)
	_ = again.Append(rec("6"))
	if got, _ := again.List(0); ids(got) != "6,5,4" {
		t.Fatalf("after reopen: %s", ids(got))
	}
}

func TestDamagedLinesSkipped(t *testing.T) {
	path := filepath.Join(t.TempDir(), "h.jsonl")
	s := New(path, 0)
	s.SetEnabled(true)
	_ = s.Append(rec("1"))
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	_, _ = f.WriteString("{\"id\":\"half-writ\n\n")
	f.Close()
	_ = s.Append(rec("2"))
	if got, err := s.List(0); err != nil || ids(got) != "2,1" {
		t.Fatalf("records %s, %v", ids(got), err)
	}
}

func TestClear(t *testing.T) {
	path := filepath.Join(t.TempDir(), "h.jsonl")
	s := New(path, 0)
	s.SetEnabled(true)
	_ = s.Append(rec("1"))
	if err := s.Clear(); err != nil {
		t.Fatal(err)
	}
	if err := s.Clear(); err != nil {
		t.Fatal("second Clear:", err)
	}
	if got, _ := s.List(0); len(got) != 0 {
		t.Fatal("records survived Clear")
	}
	if got, err := New(filepath.Join(t.TempDir(), "none"), 0).List(5); err != nil || len(got) != 0 {
		t.Fatalf("missing file: %v %v", got, err)
	}
}

func TestNoTextKept(t *testing.T) {
	path := filepath.Join(t.TempDir(), "h.jsonl")
	s := New(path, 0)
	s.SetEnabled(true)
	r := rec("clip")
	r.Text, r.Files = true, nil
	_ = s.Append(r)
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), `"text":true`) {
		t.Fatalf("text flag missing: %s", b)
	}
}

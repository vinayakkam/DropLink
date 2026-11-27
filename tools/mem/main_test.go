package main

import (
	"bytes"
	"strings"
	"testing"
)

const winCSV = `"ProcessId","ParentProcessId","Name","WorkingSetSize","PrivatePageCount"
"4","0","System","8192","0"
"100","4","droplink.exe","31457280","20971520"
"101","100","droplink-sidecar.exe","20971520","41943040"
"102","100","msedgewebview2.exe","52428800","31457280"
"103","102","msedgewebview2.exe","41943040","20971520"
"200","4","msedgewebview2.exe","99999999","99999999"
`

func TestParseWindowsAndTree(t *testing.T) {
	procs, err := ParseWindows([]byte(winCSV))
	if err != nil || len(procs) != 6 {
		t.Fatalf("%d procs, %v", len(procs), err)
	}
	tree := Tree(procs, "droplink")
	var pids []int
	for _, p := range tree {
		pids = append(pids, p.PID)
	}
	// The shell, its sidecar and its WebView (with the WebView's own
	// child) — but not another app's WebView (200).
	if len(pids) != 4 || pids[0] != 100 || pids[1] != 101 || pids[2] != 102 || pids[3] != 103 {
		t.Fatalf("tree %v", pids)
	}
	if tree[1].WorkingSet != 20971520 || tree[1].Private != 41943040 {
		t.Fatalf("sizes %+v", tree[1])
	}
}

func TestParsePS(t *testing.T) {
	out := "  1     0  1024 /sbin/init\n 50     1 30720 /opt/DropLink/droplink\n 51    50 20480 /opt/DropLink/droplink-sidecar\n garbage line\n"
	procs := ParsePS([]byte(out))
	if len(procs) != 3 || procs[1].Name != "droplink" || procs[1].WorkingSet != 30720<<10 {
		t.Fatalf("procs %+v", procs)
	}
	if tree := Tree(procs, "droplink"); len(tree) != 2 || tree[1].Name != "droplink-sidecar" {
		t.Fatalf("tree %+v", tree)
	}
}

func TestPrint(t *testing.T) {
	var b bytes.Buffer
	Print(&b, []Proc{{PID: 1, Name: "droplink.exe", WorkingSet: 30 << 20}, {PID: 2, Name: "droplink-sidecar.exe", WorkingSet: 20 << 20, Private: 10 << 20}})
	s := b.String()
	for _, want := range []string{"droplink.exe", "30.0 MiB", "10.0 MiB", "TOTAL (2 processes)", "50.0 MiB"} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in:\n%s", want, s)
		}
	}
}

func TestTreeIgnoresCycles(t *testing.T) {
	procs := []Proc{{PID: 1, PPID: 2, Name: "droplink"}, {PID: 2, PPID: 1, Name: "x"}}
	if tree := Tree(procs, "droplink"); len(tree) == 0 || len(tree) > 40 {
		t.Fatalf("tree size %d", len(tree))
	}
}

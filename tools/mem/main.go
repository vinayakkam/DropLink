// Command mem reports how much memory a running DropLink uses: the shell,
// the sidecar and the WebView processes the shell started, found by
// walking the process tree down from the shell. Run it while the app is
// open: go run ./tools/mem (or task mem).
package main

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
)

// Proc is one process.
type Proc struct {
	PID, PPID  int
	Name       string
	WorkingSet int64 // bytes in RAM
	Private    int64 // bytes committed to this process alone (Windows); 0 if unknown
}

func main() {
	root := "droplink"
	if len(os.Args) > 1 {
		root = os.Args[1]
	}
	procs, err := list()
	if err != nil {
		fmt.Fprintln(os.Stderr, "mem:", err)
		os.Exit(1)
	}
	tree := Tree(procs, root)
	if len(tree) == 0 {
		fmt.Fprintf(os.Stderr, "mem: no %q process running; start the app first\n", root)
		os.Exit(1)
	}
	Print(os.Stdout, tree)
}

func list() ([]Proc, error) {
	if runtime.GOOS == "windows" {
		out, err := exec.Command("powershell", "-NoProfile", "-Command",
			"Get-CimInstance Win32_Process | Select-Object ProcessId,ParentProcessId,Name,WorkingSetSize,PrivatePageCount | ConvertTo-Csv -NoTypeInformation").Output()
		if err != nil {
			return nil, err
		}
		return ParseWindows(out)
	}
	out, err := exec.Command("ps", "-axo", "pid=,ppid=,rss=,comm=").Output()
	if err != nil {
		return nil, err
	}
	return ParsePS(out), nil
}

// ParseWindows reads Win32_Process rows as CSV.
func ParseWindows(b []byte) ([]Proc, error) {
	rows, err := csv.NewReader(bytes.NewReader(b)).ReadAll()
	if err != nil {
		return nil, err
	}
	var out []Proc
	for i, r := range rows {
		if i == 0 || len(r) < 5 {
			continue // header
		}
		p := Proc{Name: r[2]}
		p.PID, _ = strconv.Atoi(r[0])
		p.PPID, _ = strconv.Atoi(r[1])
		p.WorkingSet, _ = strconv.ParseInt(r[3], 10, 64)
		p.Private, _ = strconv.ParseInt(r[4], 10, 64)
		out = append(out, p)
	}
	return out, nil
}

// ParsePS reads `ps -axo pid=,ppid=,rss=,comm=` (rss in KiB).
func ParsePS(b []byte) []Proc {
	var out []Proc
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) < 4 {
			continue
		}
		pid, err1 := strconv.Atoi(f[0])
		ppid, err2 := strconv.Atoi(f[1])
		rss, err3 := strconv.ParseInt(f[2], 10, 64)
		if err1 != nil || err2 != nil || err3 != nil {
			continue
		}
		name := strings.Join(f[3:], " ")
		if i := strings.LastIndex(name, "/"); i >= 0 {
			name = name[i+1:]
		}
		out = append(out, Proc{PID: pid, PPID: ppid, Name: name, WorkingSet: rss << 10})
	}
	return out
}

// Tree returns the processes named root (with or without .exe) and all
// their descendants, roots first.
func Tree(procs []Proc, root string) []Proc {
	children := map[int][]Proc{}
	for _, p := range procs {
		children[p.PPID] = append(children[p.PPID], p)
	}
	var out []Proc
	var walk func(p Proc, depth int)
	walk = func(p Proc, depth int) {
		if depth > 32 {
			return
		}
		out = append(out, p)
		kids := children[p.PID]
		sort.Slice(kids, func(i, j int) bool { return kids[i].PID < kids[j].PID })
		for _, c := range kids {
			if c.PID != p.PID {
				walk(c, depth+1)
			}
		}
	}
	for _, p := range procs {
		if strings.EqualFold(strings.TrimSuffix(p.Name, ".exe"), root) {
			walk(p, 0)
		}
	}
	return out
}

// Print writes one row per process and a total.
func Print(w io.Writer, procs []Proc) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', tabwriter.AlignRight)
	fmt.Fprintln(tw, "PROCESS\tPID\tWORKING SET\tPRIVATE\t")
	var ws, pr int64
	for _, p := range procs {
		fmt.Fprintf(tw, "%s\t%d\t%s\t%s\t\n", p.Name, p.PID, mib(p.WorkingSet), mib(p.Private))
		ws += p.WorkingSet
		pr += p.Private
	}
	fmt.Fprintf(tw, "TOTAL (%d processes)\t\t%s\t%s\t\n", len(procs), mib(ws), mib(pr))
	tw.Flush()
}

func mib(b int64) string {
	if b <= 0 {
		return "-"
	}
	return fmt.Sprintf("%.1f MiB", float64(b)/(1<<20))
}

package manager

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/vinayakkam/droplink/internal/contract"
	"github.com/vinayakkam/droplink/internal/mock"
	"github.com/vinayakkam/droplink/internal/testutil"
)

type node struct {
	m      *Manager
	peer   contract.Peer
	crypto *mock.Crypto
	out    string
	ev     <-chan Event
}

type roomTransport struct {
	contract.Transport
	members []contract.Peer
}

func (r roomTransport) RoomMembers() []contract.Peer { return r.members }

// cluster builds managers on one mock LAN; every node sees every other.
func cluster(t *testing.T, names ...string) []*node {
	t.Helper()
	n := mock.NewNetwork()
	var peers []contract.Peer
	for i, name := range names {
		peers = append(peers, contract.Peer{ID: contract.DeviceID(string(rune('a'+i)) + "0000000000000000000000000000000"), Name: name, Route: contract.RouteLAN, Addr: "lan"})
	}
	var nodes []*node
	for i, self := range peers {
		tr := n.Join(self, contract.RouteLAN)
		disc := mock.NewDiscovery()
		for j, p := range peers {
			if j != i {
				disc.Add(p)
			}
		}
		cr := mock.NewCrypto(self.ID)
		out := t.TempDir()
		m := New(Deps{Self: self, Crypto: cr, Discovery: disc, Router: &mock.Router{Transports: []contract.Transport{tr}},
			History: &mock.History{}, Transports: []contract.Transport{tr}},
			Options{DownloadDir: func() string { return out }, AutoAcceptTrusted: func() bool { return true },
				DecisionTimeout: 2 * time.Second})
		ev, cancel := m.Subscribe()
		m.Run()
		t.Cleanup(func() { cancel(); m.Close(); tr.Close() })
		nodes = append(nodes, &node{m: m, peer: self, crypto: cr, out: out, ev: ev})
	}
	return nodes
}

// waitFor reads events until f returns true for a transfer view.
func waitFor(t *testing.T, ev <-chan Event, f func(View) bool) View {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		select {
		case e := <-ev:
			if e.Transfer != nil && f(*e.Transfer) {
				return *e.Transfer
			}
		case <-deadline:
			t.Fatal("event never came")
		}
	}
}

func file(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestSendFolderAcceptAndHistory(t *testing.T) {
	testutil.NoLeaks(t)
	nodes := cluster(t, "Alpha", "Beta")
	a, b := nodes[0], nodes[1]
	src := t.TempDir()
	file(t, src, "mission/log.txt", "launch")
	file(t, src, "mission/map.bin", "0101")

	ids, err := a.m.Send(SendRequest{Peers: []contract.DeviceID{b.peer.ID}, Paths: []string{filepath.Join(src, "mission")}})
	if err != nil || len(ids) != 1 {
		t.Fatal(ids, err)
	}
	offer := waitFor(t, b.ev, func(v View) bool { return v.Waiting })
	if offer.Outgoing || offer.Files != 2 || offer.Peer.Name != "Alpha" || offer.Total != 10 {
		t.Fatalf("offer %+v", offer)
	}
	if err := b.m.Reject("nope"); err != ErrNotFound {
		t.Fatalf("reject unknown: %v", err)
	}
	if err := b.m.Accept(offer.ID, false); err != nil {
		t.Fatal(err)
	}
	if err := b.m.Accept(offer.ID, false); err != ErrNotWaiting {
		t.Fatalf("second accept: %v", err)
	}
	done := waitFor(t, a.ev, func(v View) bool { return v.State == contract.StateDone })
	if done.Done != done.Total {
		t.Fatalf("done %+v", done)
	}
	recv := waitFor(t, b.ev, func(v View) bool { return v.State == contract.StateDone })
	if recv.SavedTo == "" {
		t.Fatal("savedTo missing")
	}
	got, _ := os.ReadFile(filepath.Join(b.out, "mission", "log.txt"))
	if string(got) != "launch" {
		t.Fatalf("got %q", got)
	}
	recs, _ := a.m.d.History.List(0)
	if len(recs) != 1 || !recs[0].Outgoing || recs[0].State != contract.StateDone {
		t.Fatalf("history %+v", recs)
	}
}

func TestRejectReachesSender(t *testing.T) {
	testutil.NoLeaks(t)
	nodes := cluster(t, "Alpha", "Beta")
	a, b := nodes[0], nodes[1]
	_, _ = a.m.Send(SendRequest{Peers: []contract.DeviceID{b.peer.ID}, Text: "hello"})
	offer := waitFor(t, b.ev, func(v View) bool { return v.Waiting })
	_ = b.m.Reject(offer.ID)
	waitFor(t, a.ev, func(v View) bool { return v.State == contract.StateRejected })
}

func TestTrustedSenderIsAutoAcceptedAndTextArrives(t *testing.T) {
	testutil.NoLeaks(t)
	nodes := cluster(t, "Alpha", "Beta")
	a, b := nodes[0], nodes[1]
	_ = b.crypto.Trust(contract.TrustedDevice{ID: a.peer.ID, Name: "Alpha"})
	_, _ = a.m.Send(SendRequest{Peers: []contract.DeviceID{b.peer.ID}, Text: "dock 3"})
	v := waitFor(t, b.ev, func(v View) bool { return v.State == contract.StateDone })
	if v.Text != "dock 3" || !v.Peer.Trusted {
		t.Fatalf("view %+v", v)
	}
}

func TestAcceptWithTrustAddsToTrustList(t *testing.T) {
	testutil.NoLeaks(t)
	nodes := cluster(t, "Alpha", "Beta")
	a, b := nodes[0], nodes[1]
	_, _ = a.m.Send(SendRequest{Peers: []contract.DeviceID{b.peer.ID}, Text: "x"})
	offer := waitFor(t, b.ev, func(v View) bool { return v.Waiting })
	_ = b.m.Accept(offer.ID, true)
	waitFor(t, b.ev, func(v View) bool { return v.State == contract.StateDone })
	if !b.crypto.IsTrusted(a.peer.ID) {
		t.Fatal("sender not trusted after accept with trust")
	}
}

func TestUnansweredOfferTimesOut(t *testing.T) {
	testutil.NoLeaks(t)
	nodes := cluster(t, "Alpha", "Beta") // DecisionTimeout 2 s
	a, b := nodes[0], nodes[1]
	_, _ = a.m.Send(SendRequest{Peers: []contract.DeviceID{b.peer.ID}, Text: "anyone?"})
	waitFor(t, a.ev, func(v View) bool { return v.State == contract.StateRejected })
}

func TestCancelWhileWaiting(t *testing.T) {
	testutil.NoLeaks(t)
	nodes := cluster(t, "Alpha", "Beta")
	a, b := nodes[0], nodes[1]
	ids, _ := a.m.Send(SendRequest{Peers: []contract.DeviceID{b.peer.ID}, Text: "wait"})
	waitFor(t, b.ev, func(v View) bool { return v.Waiting })
	if err := a.m.Cancel(ids[0]); err != nil {
		t.Fatal(err)
	}
	waitFor(t, a.ev, func(v View) bool { return v.State == contract.StateCancelled })
	if err := a.m.Pause("missing"); err != ErrNotFound {
		t.Fatalf("pause unknown: %v", err)
	}
}

// The sender cancels while the receiver's offer dialog is open: the
// receiver's offer stops waiting and ends cancelled right away.
func TestWaitingOfferEndsWhenSenderCancels(t *testing.T) {
	testutil.NoLeaks(t)
	nodes := cluster(t, "Alpha", "Beta")
	a, b := nodes[0], nodes[1]
	ids, _ := a.m.Send(SendRequest{Peers: []contract.DeviceID{b.peer.ID}, Text: "never mind"})
	waitFor(t, b.ev, func(v View) bool { return v.Waiting })
	if err := a.m.Cancel(ids[0]); err != nil {
		t.Fatal(err)
	}
	v := waitFor(t, b.ev, func(v View) bool { return !v.Waiting && v.State == contract.StateCancelled })
	if v.State != contract.StateCancelled {
		t.Fatalf("receiver view %+v", v)
	}
}

func TestSendToManyPeers(t *testing.T) {
	testutil.NoLeaks(t)
	nodes := cluster(t, "Alpha", "Beta", "Gamma")
	a := nodes[0]
	for _, n := range nodes[1:] {
		_ = n.crypto.Trust(contract.TrustedDevice{ID: a.peer.ID})
	}
	ids, err := a.m.Send(SendRequest{Peers: []contract.DeviceID{nodes[1].peer.ID, nodes[2].peer.ID, nodes[1].peer.ID}, Text: "all hands"})
	if err != nil || len(ids) != 2 {
		t.Fatalf("ids %v, %v (duplicates must collapse)", ids, err)
	}
	for _, n := range nodes[1:] {
		waitFor(t, n.ev, func(v View) bool { return v.State == contract.StateDone && v.Text == "all hands" })
	}
}

func TestSendValidation(t *testing.T) {
	nodes := cluster(t, "Alpha", "Beta")
	a, b := nodes[0], nodes[1]
	for name, req := range map[string]SendRequest{
		"no peers":   {Text: "x"},
		"nothing":    {Peers: []contract.DeviceID{b.peer.ID}},
		"both":       {Peers: []contract.DeviceID{b.peer.ID}, Text: "x", Paths: []string{"y"}},
		"unknown":    {Peers: []contract.DeviceID{"ghost"}, Text: "x"},
		"no file":    {Peers: []contract.DeviceID{b.peer.ID}, Paths: []string{filepath.Join(t.TempDir(), "missing")}},
		"empty only": {Peers: []contract.DeviceID{b.peer.ID}, Paths: []string{t.TempDir()}},
	} {
		if _, err := a.m.Send(req); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestPeersMergeRadarAndRoom(t *testing.T) {
	disc := mock.NewDiscovery()
	disc.Add(contract.Peer{ID: "lan1", Name: "Nova"})
	rt := roomTransport{Transport: mock.NewNetwork().Join(contract.Peer{ID: "me"}, contract.RouteWebRTC),
		members: []contract.Peer{{ID: "me"}, {ID: "lan1", Name: "Nova"}, {ID: "far", Name: "Orbit"}}}
	defer rt.Close()
	cr := mock.NewCrypto("me")
	_ = cr.Trust(contract.TrustedDevice{ID: "far"})
	m := New(Deps{Self: contract.Peer{ID: "me"}, Crypto: cr, Discovery: disc, Transports: []contract.Transport{rt}}, Options{})
	defer m.Close()
	ps := m.Peers()
	if len(ps) != 2 || ps[0].ID != "lan1" || ps[1].ID != "far" || ps[1].Route != contract.RouteWebRTC || !ps[1].Trusted {
		t.Fatalf("peers %+v", ps)
	}
	snap := m.Snapshot()
	if snap.Type != "hello" || len(snap.Peers) != 2 {
		t.Fatalf("snapshot %+v", snap)
	}
}

func TestRadarEventsAreForwarded(t *testing.T) {
	testutil.NoLeaks(t)
	disc := mock.NewDiscovery()
	m := New(Deps{Self: contract.Peer{ID: "me"}, Crypto: mock.NewCrypto("me"), Discovery: disc}, Options{})
	ev, cancel := m.Subscribe()
	m.Run()
	disc.Add(contract.Peer{ID: "x", Name: "X"})
	select {
	case e := <-ev:
		if e.Type != "peer" || e.Kind != "joined" || e.Peer.ID != "x" {
			t.Fatalf("event %+v", e)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no peer event")
	}
	cancel()
	m.Close()
	if _, ok := <-ev; ok {
		t.Fatal("event channel open after Close")
	}
}

package api

import (
	"bufio"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vinayakkam/droplink/internal/config"
	"github.com/vinayakkam/droplink/internal/contract"
	"github.com/vinayakkam/droplink/internal/manager"
	"github.com/vinayakkam/droplink/internal/mock"
	"github.com/vinayakkam/droplink/internal/server"
	"github.com/vinayakkam/droplink/internal/testutil"
	"github.com/vinayakkam/droplink/internal/update"
)

const token = "0123456789abcdef0123456789abcdef"

type fakeRooms struct {
	code    string
	members []contract.Peer
	left    bool
}

func (f *fakeRooms) JoinRoom(_ context.Context, code string) (string, error) {
	if code == "" {
		code = "NEW234"
	}
	f.code = code
	f.members = []contract.Peer{{ID: "me"}, {ID: "x", Name: "Orbit"}}
	return code, nil
}
func (f *fakeRooms) LeaveRoom()                   { f.left, f.members = true, nil }
func (f *fakeRooms) RoomMembers() []contract.Peer { return f.members }

type env struct {
	srv      *httptest.Server
	m        *manager.Manager
	settings *SettingsStore
	crypto   *mock.Crypto
	disc     *mock.Discovery
	rooms    *fakeRooms
	applied  []Settings
}

func newEnv(t *testing.T) *env {
	t.Helper()
	dir := t.TempDir()
	cfg := config.Default()
	e := &env{crypto: mock.NewCrypto("me"), disc: mock.NewDiscovery(), rooms: &fakeRooms{}}
	st, err := LoadSettings(filepath.Join(dir, "settings.json"), DefaultSettings("node"))
	if err != nil {
		t.Fatal(err)
	}
	e.settings = st
	st.OnChange(func(s Settings) { e.applied = append(e.applied, s) })
	e.m = manager.New(manager.Deps{Self: contract.Peer{ID: "me"}, Crypto: e.crypto, Discovery: e.disc, History: &mock.History{}}, manager.Options{})
	srv := server.New(cfg, token, "test")
	Register(srv, Deps{Manager: e.m, Crypto: e.crypto, History: &mock.History{}, Settings: st, Rooms: e.rooms, Heartbeat: 50 * time.Millisecond})
	e.m.Run()
	e.srv = httptest.NewServer(srv.Handler())
	t.Cleanup(func() {
		e.srv.CloseClientConnections()
		e.srv.Close()
		e.m.Close()
	})
	return e
}

func (e *env) do(t *testing.T, method, path, body string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(method, e.srv.URL+path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := e.srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(b)
}

func TestFeaturesListed(t *testing.T) {
	e := newEnv(t)
	_, body := e.do(t, "GET", "/api/info", "")
	for _, f := range []string{"events", "peers", "send", "control", "trust", "settings", "history", "rooms"} {
		if !strings.Contains(body, `"`+f+`"`) {
			t.Errorf("feature %s missing in %s", f, body)
		}
	}
}

func TestRoutesNeedToken(t *testing.T) {
	e := newEnv(t)
	res, err := http.Get(e.srv.URL + "/api/settings")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 401 {
		t.Fatalf("status %d", res.StatusCode)
	}
}

func TestSettingsRoundTripValidateAndApply(t *testing.T) {
	e := newEnv(t)
	code, body := e.do(t, "GET", "/api/settings", "")
	if code != 200 || !strings.Contains(body, `"theme":"cyan"`) || !strings.Contains(body, `"volume":40`) {
		t.Fatalf("defaults %d %s", code, body)
	}
	code, body = e.do(t, "PUT", "/api/settings", `{"theme":"amber","speedLimitKBps":500,"name":"  Nova  "}`)
	if code != 200 || !strings.Contains(body, `"theme":"amber"`) || !strings.Contains(body, `"name":"Nova"`) || !strings.Contains(body, `"volume":40`) {
		t.Fatalf("put %d %s (omitted fields must keep their value)", code, body)
	}
	if last := e.applied[len(e.applied)-1]; last.SpeedLimitKBps != 500 {
		t.Fatalf("not applied: %+v", last)
	}
	for _, bad := range []string{`{"theme":"neon"}`, `{"volume":101}`, `{"avatar":16}`, `{"downloadDir":"relative/dir"}`, `{"name":""}`, `not json`} {
		if code, _ := e.do(t, "PUT", "/api/settings", bad); code != 400 {
			t.Errorf("%s accepted (%d)", bad, code)
		}
	}
	// Saved to disk: a new store reads the same values.
	again, err := LoadSettings(e.settings.path, DefaultSettings("other"))
	if err != nil || again.Get().Theme != "amber" || again.Get().Name != "Nova" {
		t.Fatalf("reload %+v %v", again.Get(), err)
	}
}

func TestDamagedSettingsReported(t *testing.T) {
	p := filepath.Join(t.TempDir(), "settings.json")
	_ = os.WriteFile(p, []byte("{"), 0o600)
	if _, err := LoadSettings(p, DefaultSettings("n")); err == nil {
		t.Fatal("damaged settings accepted")
	}
	_ = os.WriteFile(p, []byte(`{"theme":"neon"}`), 0o600)
	st, err := LoadSettings(p, DefaultSettings("n"))
	if err != nil || st.Get().Theme != "cyan" {
		t.Fatalf("invalid file should fall back to defaults: %+v %v", st.Get(), err)
	}
}

func TestTrustRoutes(t *testing.T) {
	e := newEnv(t)
	if code, _ := e.do(t, "PUT", "/api/trust/dev1", `{"name":"Vega"}`); code != 204 {
		t.Fatalf("trust %d", code)
	}
	_, body := e.do(t, "GET", "/api/trust", "")
	if !strings.Contains(body, `"name":"Vega"`) {
		t.Fatalf("list %s", body)
	}
	if code, _ := e.do(t, "DELETE", "/api/trust/dev1", ""); code != 204 || e.crypto.IsTrusted("dev1") {
		t.Fatal("untrust failed")
	}
}

func TestTransferErrors(t *testing.T) {
	e := newEnv(t)
	for _, tc := range []struct {
		method, path, body string
		want               int
	}{
		{"POST", "/api/transfers", `{"peers":["ghost"],"text":"x"}`, 400},
		{"POST", "/api/transfers", `{"peers":[]}`, 400},
		{"POST", "/api/transfers", `{`, 400},
		{"POST", "/api/transfers/nope/accept", "", 404},
		{"POST", "/api/transfers/nope/explode", "", 404},
		{"GET", "/api/transfers", "", 200},
		{"GET", "/api/peers", "", 200},
	} {
		if code, body := e.do(t, tc.method, tc.path, tc.body); code != tc.want {
			t.Errorf("%s %s: %d %s", tc.method, tc.path, code, body)
		}
	}
	big := `{"peers":["x"],"text":"` + strings.Repeat("a", 3<<20) + `"}`
	if code, _ := e.do(t, "POST", "/api/transfers", big); code != 400 {
		t.Errorf("3 MiB body accepted: %d", code)
	}
}

func TestHistoryRoutes(t *testing.T) {
	e := newEnv(t)
	code, body := e.do(t, "GET", "/api/history?limit=5", "")
	if code != 200 || strings.TrimSpace(body) != "[]" {
		t.Fatalf("empty history: %d %q", code, body)
	}
	if code, _ := e.do(t, "DELETE", "/api/history", ""); code != 204 {
		t.Fatalf("clear %d", code)
	}
}

func TestRooms(t *testing.T) {
	e := newEnv(t)
	code, body := e.do(t, "POST", "/api/rooms", "{}")
	if code != 200 || !strings.Contains(body, `"code":"NEW234"`) || !strings.Contains(body, "Orbit") {
		t.Fatalf("create %d %s", code, body)
	}
	if code, body = e.do(t, "POST", "/api/rooms/ABC234/join", ""); !strings.Contains(body, "ABC234") {
		t.Fatalf("join %d %s", code, body)
	}
	if code, _ := e.do(t, "DELETE", "/api/rooms/current", ""); code != 204 || !e.rooms.left {
		t.Fatal("leave failed")
	}
	_, body = e.do(t, "GET", "/api/rooms", "")
	if !strings.Contains(body, `"code":""`) {
		t.Fatalf("after leave %s", body)
	}
}

func TestEventStream(t *testing.T) {
	testutil.NoLeaks(t)
	e := newEnv(t)
	e.disc.Add(contract.Peer{ID: "p1", Name: "Nova"})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", e.srv.URL+"/api/events?token="+token, nil)
	res, err := e.srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if ct := res.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content type %q", ct)
	}
	rd := bufio.NewReader(res.Body)
	readEvent := func() (manager.Event, bool) {
		for {
			line, err := rd.ReadString('\n')
			if err != nil {
				t.Fatal(err)
			}
			if strings.HasPrefix(line, ": ping") {
				return manager.Event{}, true
			}
			if data, ok := strings.CutPrefix(strings.TrimSpace(line), "data: "); ok {
				var ev manager.Event
				if err := json.Unmarshal([]byte(data), &ev); err != nil {
					t.Fatal(err)
				}
				return ev, false
			}
		}
	}
	first, _ := readEvent()
	if first.Type != "hello" || len(first.Peers) != 1 || first.Peers[0].Name != "Nova" {
		t.Fatalf("first event %+v", first)
	}
	e.disc.Add(contract.Peer{ID: "p2", Name: "Orbit"})
	sawPeer, sawPing := false, false
	for i := 0; i < 10 && !(sawPeer && sawPing); i++ {
		ev, ping := readEvent()
		sawPing = sawPing || ping
		sawPeer = sawPeer || (ev.Type == "peer" && ev.Peer != nil && ev.Peer.ID == "p2")
	}
	if !sawPeer || !sawPing {
		t.Fatalf("peer event %v, heartbeat %v", sawPeer, sawPing)
	}
	cancel()
}

func TestUpdateRoutes(t *testing.T) {
	testutil.NoLeaks(t)
	// A signed release on a local server.
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	installer := []byte("setup-bytes")
	sum := sha256.Sum256(installer)
	hexSum := hex.EncodeToString(sum[:])
	mux := http.NewServeMux()
	rel := httptest.NewServer(mux)
	defer rel.Close()
	mux.HandleFunc("/latest.json", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(update.Manifest{Version: "9.0.0", Notes: "big", Platforms: map[string]update.Asset{
			"windows-x86_64": {URL: rel.URL + "/setup.exe", Size: int64(len(installer)), SHA256: hexSum, Signature: update.Sign(priv, "9.0.0", hexSum)},
		}})
	})
	mux.HandleFunc("/setup.exe", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(installer) })

	c := update.New("1.0.0", t.TempDir())
	c.URL, c.PublicKey, c.Platform = rel.URL+"/latest.json", base64.StdEncoding.EncodeToString(pub), "windows-x86_64"
	m := manager.New(manager.Deps{Self: contract.Peer{ID: "me"}}, manager.Options{})
	defer m.Close()
	srv := server.New(config.Default(), token, "test")
	Register(srv, Deps{Manager: m, Crypto: mock.NewCrypto("me"), History: &mock.History{},
		Settings: mustSettings(t), Updater: &Updater{Client: c, Events: m}})
	api := httptest.NewServer(srv.Handler())
	defer api.Close()
	do := func(method, path string) (int, string) {
		req, _ := http.NewRequest(method, api.URL+path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		res, err := api.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, string(b)
	}

	if code, _ := do("POST", "/api/update/download"); code != 409 {
		t.Fatalf("download before check: %d", code)
	}
	ev, cancel := m.Subscribe()
	defer cancel()
	code, body := do("GET", "/api/update")
	if code != 200 || !strings.Contains(body, `"available":true`) || !strings.Contains(body, `"latest":"9.0.0"`) {
		t.Fatalf("check %d %s", code, body)
	}
	if code, _ := do("POST", "/api/update/download"); code != 202 {
		t.Fatalf("download %d", code)
	}
	deadline := time.After(5 * time.Second)
	for {
		select {
		case e := <-ev:
			if s, ok := e.Update.(UpdateState); ok && s.Phase == "ready" {
				got, _ := os.ReadFile(filepath.Join(c.Dir, update.InstallerFile))
				if string(got) != "setup-bytes" {
					t.Fatalf("installer %q", got)
				}
				return
			}
		case <-deadline:
			t.Fatal("no ready event")
		}
	}
}

func mustSettings(t *testing.T) *SettingsStore {
	t.Helper()
	st, err := LoadSettings(filepath.Join(t.TempDir(), "s.json"), DefaultSettings("n"))
	if err != nil {
		t.Fatal(err)
	}
	return st
}

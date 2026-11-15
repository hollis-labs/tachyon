package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/hollis-labs/substrate/modules/database"
	"github.com/hollis-labs/substrate/modules/identity"

	"github.com/hollis-labs/tachyon/next/internal/transcripts"
)

// transcriptsOverride points Fragments Engine at url with a synthetic credential (a file:// secrets
// reference written for the test), the transcripts switch on or off.
func transcriptsOverride(t *testing.T, url string, on bool) string {
	t.Helper()
	file := filepath.Join(t.TempDir(), "fragments.token")
	if err := os.WriteFile(file, []byte("synthetic-fragments-"+t.Name()), 0o600); err != nil {
		t.Fatal(err)
	}
	return loopbackOverride + fmt.Sprintf(`tachyon:
  upstreams:
    fragments: { url: %q, token: %q }
  features: { transcripts: %t }
`, url, "file://"+file, on)
}

// fakeFragments is a Fragments Engine with two sessions; down makes it answer 503 to everything.
type fakeFragments struct {
	*httptest.Server
	down  atomic.Bool
	reads atomic.Int64
}

func newFakeFragments(t *testing.T) *fakeFragments {
	t.Helper()
	f := &fakeFragments{}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.reads.Add(1)
		if f.down.Load() {
			w.WriteHeader(503)
			return
		}
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer synthetic-fragments-") || r.Header.Get("Cookie") != "" {
			w.WriteHeader(401)
			return
		}
		id := strings.Repeat("a", 64)
		switch p := r.URL.Path; {
		case p == "/v1/transcripts/status":
			_, _ = w.Write([]byte(`{"available":true,"enabled":true,"modes":["text"],"features":["errors"]}`))
		case p == "/v1/transcripts/facets":
			_, _ = w.Write([]byte(`{"total":2,"tools":["codex"],"models":[]}`))
		case p == "/v1/transcripts/sessions" && r.URL.Query().Get("q") != "":
			_, _ = w.Write([]byte(`{"items":[{"id":"` + id + `"}],"next_cursor":""}`))
		case p == "/v1/transcripts/sessions":
			_, _ = w.Write([]byte(`{"items":[{"id":"` + id + `"},{"id":"s2"}],"next_cursor":""}`))
		case p == "/v1/transcripts/sessions/"+id:
			_, _ = w.Write([]byte(`{"id":"` + id + `","turns":[]}`))
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(f.Close)
	return f
}

func transcriptPaths() []string {
	p, id := transcripts.Prefix, strings.Repeat("a", 64)
	return []string{p + "/snapshot", p + "/status", p + "/search?q=x", p + "/stats", p + "/sessions/" + id,
		p + "/sessions/" + id + "/find?q=x", p + "/sessions/" + id + "/errors"}
}

// The transcript routes in the served binary (slice B10) answer only a signed-in person's browser
// session. Anonymous is 401 (the framework guard). A key without tachyon.transcripts.read is refused by
// the route scope first (scope_required, the framework); a key holding it passes the scope and is then
// refused by the transcripts guard (403 forbidden, as the old guard refused any bearer), and so is the
// owner's own browser sending a key. The signed-in owner reaches the bridge.
func TestTranscriptsRoutesAuth(t *testing.T) {
	fe := newFakeFragments(t)
	base, app := started(t, transcriptsOverride(t, fe.URL, true))
	ids := identity.From(app)
	key := func(scopes ...string) map[string]string {
		k, _, err := ids.CreateKey(context.Background(), identity.KeyInput{Name: "transcripts-test", Scopes: scopes})
		if err != nil {
			t.Fatal(err)
		}
		return map[string]string{"Authorization": "Bearer " + k}
	}
	reader, other, all := key("tachyon.transcripts.read"), key("tachyon.planner.read"), key("*")
	anon, keys, owner := newClient(t, base), newClient(t, base), newClient(t, base)
	owner.want(200, "POST", "/api/login", map[string]string{"username": "owner", "password": ownerPassword}, xrw)
	// The guard tells the browser session's own bearer by the session cookie's name (the identity module's default,
	// "<app>_session", which substrate does not export). If that name changes, this fails before the guard silently
	// refuses every browser.
	u, _ := url.Parse(base)
	named := false
	for _, c := range owner.http.Jar.Cookies(u) {
		named = named || c.Name == app.Name+"_session"
	}
	if !named {
		t.Fatalf("no %s_session cookie after sign-in: the transcripts guard's cookie name is out of date", app.Name)
	}
	for _, path := range transcriptPaths() {
		anon.want(401, "GET", path, nil, xrw)
		if code := flatCode(t, keys.want(403, "GET", path, nil, other)); code != "scope_required" {
			t.Errorf("key without the scope %s: %s", path, code)
		}
		for name, h := range map[string]map[string]string{"reader key": reader, "* key": all} {
			if code := flatCode(t, keys.want(403, "GET", path, nil, h)); code != "forbidden" {
				t.Errorf("%s %s: %s", name, path, code)
			}
			if code := flatCode(t, owner.want(403, "GET", path, nil, h)); code != "forbidden" {
				t.Errorf("owner's browser with a %s %s: %s", name, path, code)
			}
		}
	}
	// A token the owner's session mints for itself is not the browser session either.
	var minted struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(owner.want(200, "POST", "/api/token", map[string]any{"scopes": []string{"tachyon.transcripts.read"}}, xrw), &minted); err != nil || minted.Token == "" {
		t.Fatalf("mint a token: %v", err)
	}
	for _, path := range transcriptPaths() {
		if code := flatCode(t, keys.want(403, "GET", path, nil, map[string]string{"Authorization": "Bearer " + minted.Token})); code != "forbidden" {
			t.Errorf("minted token %s: %s", path, code)
		}
	}
	if n := fe.reads.Load(); n != 0 {
		t.Fatalf("a refused request reached Fragments Engine (%d reads)", n)
	}
	owner.want(200, "GET", transcripts.Prefix+"/status", nil, nil)
	owner.want(200, "GET", transcripts.Prefix+"/snapshot", nil, nil)
	if code := flatCode(t, owner.want(404, "GET", transcripts.Prefix+"/sessions/not-an-id", nil, nil)); code != "not_found" {
		t.Errorf("bad session id: %s", code)
	}
}

// Switch off: no connection to Fragments Engine is ever attempted, on any route. The snapshot is the
// saved row when there is one, else 503 disabled; everything else is 503 disabled.
func TestTranscriptsSwitchOff(t *testing.T) {
	up := countingListener(t)
	base, app := started(t, transcriptsOverride(t, up.addr, false))
	owner := newClient(t, base)
	owner.want(200, "POST", "/api/login", map[string]string{"username": "owner", "password": ownerPassword}, xrw)
	for _, path := range transcriptPaths() {
		if code := flatCode(t, owner.want(503, "GET", path, nil, nil)); code != "disabled" {
			t.Errorf("%s: %s", path, code)
		}
	}
	if _, err := database.From(app).ExecContext(context.Background(), `INSERT INTO transcript_snapshot(id, body, observed_ms) VALUES (1, ?, ?)`,
		[]byte(`{"sessions":[{"id":"saved"}],"snapshot_meta":{"source_state":"live"}}`), int64(1700000000000)); err != nil {
		t.Fatal(err)
	}
	var saved struct {
		Sessions []struct{ ID string } `json:"sessions"`
		Meta     struct {
			SourceState string            `json:"source_state"`
			Error       map[string]string `json:"error"`
		} `json:"snapshot_meta"`
	}
	body := owner.want(200, "GET", transcripts.Prefix+"/snapshot", nil, nil)
	if err := json.Unmarshal(body, &saved); err != nil || len(saved.Sessions) != 1 || saved.Meta.SourceState != "unavailable" || saved.Meta.Error["code"] != "disabled" {
		t.Errorf("saved snapshot: %s", body)
	}
	for _, path := range transcriptPaths()[1:] {
		if code := flatCode(t, owner.want(503, "GET", path, nil, nil)); code != "disabled" {
			t.Errorf("%s with a saved snapshot: %s", path, code)
		}
	}
	if n := up.conns.Load(); n != 0 {
		t.Fatalf("switch off: %d connections to Fragments Engine", n)
	}
}

// Switch on, with a Fragments Engine: the snapshot (saved as it is read), search, a session, an
// upstream 404 mapped to not_found, and the saved snapshot served when Fragments Engine is down.
func TestTranscriptsSwitchOn(t *testing.T) {
	fe := newFakeFragments(t)
	base, _ := started(t, transcriptsOverride(t, fe.URL, true))
	owner := newClient(t, base)
	owner.want(200, "POST", "/api/login", map[string]string{"username": "owner", "password": ownerPassword}, xrw)
	p, id := transcripts.Prefix, strings.Repeat("a", 64)
	var snap struct {
		Sessions []struct{ ID string } `json:"sessions"`
		Complete bool                  `json:"complete"`
		Meta     struct {
			SourceState string `json:"source_state"`
			Limit       int    `json:"limit"`
		} `json:"snapshot_meta"`
	}
	body := owner.want(200, "GET", p+"/snapshot", nil, nil)
	if err := json.Unmarshal(body, &snap); err != nil || len(snap.Sessions) != 2 || !snap.Complete || snap.Meta.SourceState != "live" || snap.Meta.Limit != transcripts.MaxSessions {
		t.Fatalf("snapshot: %s", body)
	}
	if body = owner.want(200, "GET", p+"/search?q=deploy", nil, nil); !strings.Contains(string(body), id) || !strings.Contains(string(body), `"mode":"text"`) {
		t.Errorf("search: %s", body)
	}
	if code := flatCode(t, owner.want(400, "GET", p+"/search?q=deploy&mode=semantic", nil, nil)); code != "invalid_args" {
		t.Errorf("unavailable mode: %s", code)
	}
	if body = owner.want(200, "GET", p+"/sessions/"+id+"?turn_limit=10", nil, nil); !strings.Contains(string(body), `"turns":[]`) {
		t.Errorf("session: %s", body)
	}
	if code := flatCode(t, owner.want(404, "GET", p+"/sessions/"+strings.Repeat("f", 64), nil, nil)); code != "not_found" {
		t.Errorf("unknown session: %s", code)
	}
	// errors is a feature of this build, so its 404 is an unknown session; stats is not, so 501.
	if code := flatCode(t, owner.want(404, "GET", p+"/sessions/"+strings.Repeat("f", 64)+"/errors", nil, nil)); code != "not_found" {
		t.Errorf("unknown session errors: %s", code)
	}
	if code := flatCode(t, owner.want(501, "GET", p+"/stats", nil, nil)); code != "unsupported" {
		t.Errorf("stats on a build without it: %s", code)
	}
	fe.down.Store(true)
	body = owner.want(200, "GET", p+"/snapshot", nil, nil)
	var saved struct {
		Sessions []json.RawMessage `json:"sessions"`
		Meta     struct {
			SourceState string            `json:"source_state"`
			Error       map[string]string `json:"error"`
		} `json:"snapshot_meta"`
	}
	if err := json.Unmarshal(body, &saved); err != nil || len(saved.Sessions) != 2 || saved.Meta.SourceState != "unavailable" || saved.Meta.Error["code"] != "remote" {
		t.Errorf("saved snapshot while down: %s", body)
	}
	if code := flatCode(t, owner.want(502, "GET", p+"/status", nil, nil)); code != "remote" {
		t.Errorf("status while down: %s", code)
	}
}

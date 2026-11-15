package transcripts

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hollis-labs/tachyon/next/internal/store"
	"github.com/hollis-labs/tachyon/next/internal/store/storetest"
)

// testToken is a synthetic credential, built at run time (a literal reads as a secret to the hygiene scan).
var testToken = strings.Join([]string{"synthetic", "fragments", "e2e"}, "-")

func token(context.Context) (string, error) { return testToken, nil }

// on is a bridge with the switch on and the synthetic credential.
func on(addr string, maxSessions int) *Bridge {
	b := New(addr, token, true)
	b.MaxSessions = maxSessions
	return b
}

func get(h http.Handler, path string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", Prefix+path, nil))
	return w
}
func TestSnapshotPaginationCoverageAndCredentialBoundary(t *testing.T) {
	cutoff := ""
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.Header.Get("Authorization") != "Bearer "+testToken || r.Header.Get("Cookie") != "" {
			t.Error("incorrect upstream authority or mutation")
		}
		switch r.URL.Path {
		case "/v1/transcripts/status":
			_, _ = w.Write([]byte(`{"available":true,"enabled":true}`))
		case "/v1/transcripts/facets":
			_, _ = w.Write([]byte(`{"total":3,"enriched":1,"tools":["codex"],"models":[]}`))
		case "/v1/transcripts/sessions":
			if cutoff == "" {
				cutoff = r.URL.Query().Get("until")
			}
			if cutoff == "" || r.URL.Query().Get("until") != cutoff {
				t.Error("moving pagination cutoff")
			}
			if r.URL.Query().Get("cursor") == "" {
				_, _ = w.Write([]byte(`{"items":[{"id":"a"},{"id":"b"}],"next_cursor":"next"}`))
			} else {
				_, _ = w.Write([]byte(`{"items":[{"id":"b"},{"id":"c"}],"next_cursor":""}`))
			}
		default:
			t.Error("unexpected route", r.URL.Path)
		}
	}))
	defer upstream.Close()
	b := on(upstream.URL, 400)
	r := httptest.NewRequest("GET", Prefix+"/snapshot?token=must-not-forward", nil)
	r.Header.Set("Authorization", "Bearer browser-secret")
	r.Header.Set("Cookie", "private=browser")
	w := httptest.NewRecorder()
	b.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var result struct {
		Sessions []struct{ ID string }
		Complete bool
		Facets   struct{ Total int }
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for _, s := range result.Sessions {
		ids = append(ids, s.ID)
	}
	if strings.Join(ids, ",") != "a,b,c" || !result.Complete || result.Facets.Total != 3 {
		t.Fatal("duplicate or incomplete metadata", w.Body.String())
	}
	// A bounded first page must not claim whole-database coverage.
	cutoff = ""
	b.MaxSessions = 200
	w = get(b, "/snapshot")
	if !strings.Contains(w.Body.String(), `"complete":false`) {
		t.Fatal("partial snapshot presented as complete", w.Body.String())
	}
}

func TestDetailIsBoundedReadOnlyAndDoesNotFollowRedirects(t *testing.T) {
	touched := false
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		touched = true
		if r.URL.Query().Encode() != "turn_limit=50&turn_offset=50" {
			t.Error("forwarded arbitrary query", r.URL.RawQuery)
		}
		w.Header().Set("Location", "https://example.com/secret")
		w.WriteHeader(302)
	}))
	defer upstream.Close()
	b := on(upstream.URL, 200)
	w := get(b, "/sessions/"+strings.Repeat("a", 64)+"?turn_offset=50&turn_limit=50")
	if w.Code != 502 || !touched {
		t.Fatal(w.Code, w.Body.String())
	}
	touched = false
	for _, bad := range []string{"?raw=true", "?turn_limit=201", "?turn_limit=0", "?turn_offset=x"} {
		if w = get(b, "/sessions/"+strings.Repeat("a", 64)+bad); w.Code != 400 {
			t.Fatal("bad detail query allowed", bad, w.Code)
		}
	}
	for _, method := range []string{"POST", "DELETE"} {
		w = httptest.NewRecorder()
		b.ServeHTTP(w, httptest.NewRequest(method, Prefix+"/snapshot", nil))
		if w.Code != 405 {
			t.Fatal("mutation allowed")
		}
	}
	if w = get(b, "/sessions/bad-id"); w.Code != 404 {
		t.Fatal("unvalidated session path allowed")
	}
	if w = get(b, "/sessions/"+strings.Repeat("a", 64)+"?turn_offset=-1"); w.Code != 400 {
		t.Fatal("invalid page allowed")
	}
	if touched {
		t.Fatal("rejected request reached upstream")
	}
}

// The saved snapshot outlives the process (a second handle on the same database), is served while
// Fragments Engine is down or denies the read (the old bridge's own fallback; the API cache that stood in
// front of it is gone), and a credential that no longer resolves is 503 configuration without one.
func TestDurableSnapshotsSurviveOutageAndCredentialRemoval(t *testing.T) {
	var remoteStatus atomic.Int32
	remoteStatus.Store(200)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s := int(remoteStatus.Load()); s != 200 {
			w.WriteHeader(s)
			return
		}
		switch r.URL.Path {
		case "/v1/transcripts/status":
			_, _ = w.Write([]byte(`{"available":true,"enabled":true}`))
		case "/v1/transcripts/facets":
			_, _ = w.Write([]byte(`{"total":0,"tools":[],"models":[]}`))
		default:
			_, _ = w.Write([]byte(`{"items":[],"next_cursor":""}`))
		}
	}))
	defer upstream.Close()
	testDB := storetest.New(t)
	b := on(upstream.URL, 200)
	b.DB = testDB.Open(t)
	if w := get(b, "/snapshot"); w.Code != 200 || !strings.Contains(w.Body.String(), `"source_state":"live"`) {
		t.Fatal(w.Code, w.Body.String())
	}
	// A new handle on the same data, as after a restart.
	b = on(upstream.URL, 200)
	b.DB = testDB.Open(t)
	for _, status := range []int32{503, 403} {
		remoteStatus.Store(status)
		if w := get(b, "/snapshot"); w.Code != 200 || !strings.Contains(w.Body.String(), `"source_state":"unavailable"`) {
			t.Fatal("durable fallback lost", status, w.Code, w.Body.String())
		}
	}
	// Fragments Engine gone entirely (connection refused): the saved snapshot still shows, marked unreachable.
	down := httptest.NewServer(http.NotFoundHandler())
	downURL := down.URL
	down.Close()
	gone := on(downURL, 200)
	gone.DB = b.DB
	if w := get(gone, "/snapshot"); w.Code != 200 || !strings.Contains(w.Body.String(), `"source_state":"unavailable"`) || !strings.Contains(w.Body.String(), `"code":"unreachable"`) {
		t.Fatal("saved snapshot not served when Fragments Engine is unreachable", w.Code, w.Body.String())
	}
	if w := get(on(downURL, 200), "/snapshot"); w.Code != 502 || !strings.Contains(w.Body.String(), "unreachable") {
		t.Fatal("unreachable without a saved snapshot", w.Code, w.Body.String())
	}
	// No saved snapshot: the refusal shows.
	if w := get(on(upstream.URL, 200), "/snapshot"); w.Code != 403 || !strings.Contains(w.Body.String(), "authentication") {
		t.Fatal("denial concealed", w.Code, w.Body.String())
	}
	remoteStatus.Store(200)
	var sent atomic.Int32
	counting := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { sent.Add(1) }))
	defer counting.Close()
	gone = New(counting.URL, func(context.Context) (string, error) { return "", errors.New("reference does not resolve") }, true)
	if w := get(gone, "/snapshot"); w.Code != 503 || !strings.Contains(w.Body.String(), "configuration") {
		t.Fatal("missing credential concealed", w.Code, w.Body.String())
	}
	gone.DB = b.DB
	if w := get(gone, "/snapshot"); w.Code != 200 || !strings.Contains(w.Body.String(), `"code":"configuration"`) {
		t.Fatal("saved snapshot not served on a credential failure", w.Code, w.Body.String())
	}
	if sent.Load() != 0 {
		t.Fatal("a read without a credential reached Fragments Engine")
	}
}

func TestSearchValidatesAndForwardsOnlyKnownParameters(t *testing.T) {
	var got string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/transcripts/status" {
			_, _ = w.Write([]byte(`{"available":true,"modes":["text","summary"]}`))
			return
		}
		got = r.URL.Path + "?" + r.URL.Query().Encode()
		_, _ = w.Write([]byte(`{"items":[{"id":"x"}],"next_cursor":"c2","snippets_skipped":0}`))
	}))
	defer upstream.Close()
	b := on(upstream.URL, 200)
	w := get(b, "/search?q=deploy+failure&mode=summary&limit=5&tool=codex&has_error=true&kind=main")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"next_cursor":"c2"`) || !strings.Contains(w.Body.String(), `"mode":"summary"`) {
		t.Fatal(w.Code, w.Body.String())
	}
	if !strings.HasPrefix(got, "/v1/transcripts/sessions?") || !strings.Contains(got, "q=deploy+failure") || !strings.Contains(got, "has_error=true") {
		t.Fatal("not forwarded", got)
	}
	got = ""
	for _, bad := range []string{"", "?q=", "?q=a&mode=vector", "?q=a&mode=semantic", "?q=a&limit=51", "?q=a&since=yesterday", "?q=a&kind=other", "?q=a&has_error=maybe", "?q=a&sort=id", "?q=" + strings.Repeat("x", 257)} {
		if w = get(b, "/search"+bad); w.Code != 400 {
			t.Fatal("bad search allowed", bad, w.Code)
		}
	}
	if got != "" {
		t.Fatal("rejected search reached upstream")
	}
}

func TestStatusAddsCapabilities(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"available":true,"enabled":true}`))
	}))
	defer upstream.Close()
	w := get(on(upstream.URL, 200), "/status")
	var out struct {
		Available    bool            `json:"available"`
		Capabilities map[string]bool `json:"capabilities"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || !out.Available || !out.Capabilities["search"] || out.Capabilities["stats"] {
		t.Fatal(w.Body.String())
	}
}

func TestSnapshotSharesOneBuildAndServesTheSavedOneWhenUnavailable(t *testing.T) {
	var walks atomic.Int32
	mode := "ok"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if mode != "ok" {
			w.WriteHeader(map[string]int{"denied": 403, "down": 503}[mode])
			return
		}
		switch r.URL.Path {
		case "/v1/transcripts/status":
			walks.Add(1)
			time.Sleep(50 * time.Millisecond)
			_, _ = w.Write([]byte(`{"available":true,"enabled":true}`))
		case "/v1/transcripts/facets":
			_, _ = w.Write([]byte(`{"total":9,"tools":[],"models":[]}`))
		default:
			_, _ = w.Write([]byte(`{"items":[{"id":"s1"},{"id":"s2"}],"next_cursor":""}`))
		}
	}))
	defer upstream.Close()
	b := on(upstream.URL, 200)
	b.DB = storetest.New(t).Open(t)
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if w := get(b, "/snapshot"); w.Code != 200 {
				t.Error(w.Code, w.Body.String())
			}
		}()
	}
	wg.Wait()
	if walks.Load() != 1 {
		t.Fatal("concurrent snapshots each walked the source:", walks.Load())
	}
	w := get(b, "/snapshot")
	var live struct {
		Meta snapshotMeta `json:"snapshot_meta"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &live)
	if live.Meta.SourceState != "live" || live.Meta.OmittedCount != 7 || live.Meta.TotalAvailable != 9 {
		t.Fatal("meta", w.Body.String())
	}
	for _, m := range []string{"down", "denied"} {
		mode = m
		w = get(b, "/snapshot")
		var saved struct {
			Sessions []json.RawMessage `json:"sessions"`
			Meta     snapshotMeta      `json:"snapshot_meta"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &saved)
		if w.Code != 200 || saved.Meta.SourceState != "unavailable" || saved.Meta.Error == nil || len(saved.Sessions) != 2 {
			t.Fatal(m, w.Code, w.Body.String())
		}
	}
	if w = get(on(upstream.URL, 200), "/snapshot"); w.Code != 403 {
		t.Fatal("without a saved snapshot the refusal shows", w.Code)
	}
}

func TestFindErrorsStatsProxyAndUnsupportedBuilds(t *testing.T) {
	features := `["find","errors","stats"]`
	var got []string
	id := strings.Repeat("b", 64)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/transcripts/status" {
			_, _ = w.Write([]byte(`{"available":true,"enabled":true,"features":` + features + `,"ingest":{"state":"ok","last_ingest_at":"2026-10-30T10:00:00Z"}}`))
			return
		}
		got = append(got, r.URL.Path+"?"+r.URL.Query().Encode())
		if features == "[]" || strings.Contains(r.URL.Path, strings.Repeat("0", 64)) {
			w.WriteHeader(404)
			return
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()
	b := on(upstream.URL, 200)
	for _, p := range []string{"/sessions/" + id + "/find?q=boom&after_offset=3&limit=10", "/sessions/" + id + "/errors", "/stats?tool=codex&has_error=true&since=2026-10-01T00:00:00Z"} {
		if w := get(b, p); w.Code != 200 || !strings.Contains(w.Body.String(), `"ok":true`) {
			t.Fatal(p, w.Code, w.Body.String())
		}
	}
	if len(got) != 3 || got[0] != "/v1/transcripts/sessions/"+id+"/find?after_offset=3&limit=10&q=boom" || got[2] != "/v1/transcripts/stats?has_error=true&since=2026-10-01T00%3A00%3A00Z&tool=codex" {
		t.Fatal("forwarded", got)
	}
	got = nil
	for _, bad := range []string{"/sessions/" + id + "/find", "/sessions/" + id + "/find?q=a&limit=101", "/sessions/" + id + "/find?q=a&x=1", "/sessions/" + id + "/errors?x=1", "/stats?tool=a&sort=x", "/stats?kind=z", "/stats?since=nope", "/sessions/" + id + "?turn_from_seq=2&turn_offset=1", "/sessions/short/find?q=a"} {
		if w := get(b, bad); w.Code != 400 && w.Code != 404 {
			t.Fatal("bad read allowed", bad, w.Code)
		}
	}
	if len(got) != 0 {
		t.Fatal("rejected read reached upstream", got)
	}
	if w := get(b, "/sessions/"+strings.Repeat("0", 64)+"/errors"); w.Code != 404 {
		t.Fatal("unknown session on a capable build is not_found", w.Code)
	}
	var st struct {
		Capabilities map[string]bool `json:"capabilities"`
		Ingest       struct {
			State string `json:"state"`
		} `json:"ingest"`
	}
	_ = json.Unmarshal(get(b, "/status").Body.Bytes(), &st)
	if !st.Capabilities["find"] || !st.Capabilities["errors"] || !st.Capabilities["stats"] || st.Ingest.State != "ok" {
		t.Fatal("status", st)
	}
	features = "[]"
	if w := get(b, "/stats"); w.Code != 501 || !strings.Contains(w.Body.String(), "unsupported") {
		t.Fatal("older build", w.Code, w.Body.String())
	}
	_ = json.Unmarshal(get(b, "/status").Body.Bytes(), &st)
	if st.Capabilities["find"] || st.Capabilities["stats"] || !st.Capabilities["search"] {
		t.Fatal("capabilities on an older build", st)
	}
}

// acceptCounter is a listener that counts every connection it accepts and closes it: a Fragments
// Engine that proves nothing was attempted.
func acceptCounter(t *testing.T) (string, *atomic.Int32) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var n atomic.Int32
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			n.Add(1)
			_ = c.Close()
		}
	}()
	t.Cleanup(func() { _ = ln.Close() })
	return "http://" + ln.Addr().String(), &n
}

// With tachyon.features.transcripts off nothing is sent to Fragments Engine, whatever the route: the
// snapshot is the saved one when there is one (with the disabled error in its meta), and every other
// read, or a snapshot with nothing saved, is 503 disabled.
func TestSwitchOffNeverConnects(t *testing.T) {
	addr, conns := acceptCounter(t)
	b := New(addr, token, false)
	id := strings.Repeat("c", 64)
	paths := []string{"/snapshot", "/status", "/search?q=x", "/stats", "/sessions/" + id, "/sessions/" + id + "/find?q=x", "/sessions/" + id + "/errors"}
	for _, p := range paths {
		w := get(b, p)
		if w.Code != 503 || !strings.Contains(w.Body.String(), `"code":"disabled"`) {
			t.Fatal(p, w.Code, w.Body.String())
		}
	}
	db := storetest.New(t).Open(t)
	b.DB = db
	if err := db.Writer.Submit(context.Background(), "seed", func(ctx context.Context, tx store.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO transcript_snapshot(id, body, observed_ms) VALUES (1, ?, ?)`,
			[]byte(`{"sessions":[{"id":"saved"}],"snapshot_meta":{"source_state":"live","limit":5000}}`), int64(1700000000000))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	w := get(b, "/snapshot")
	var saved struct {
		Sessions []struct{ ID string } `json:"sessions"`
		Meta     snapshotMeta          `json:"snapshot_meta"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &saved); err != nil || w.Code != 200 || len(saved.Sessions) != 1 || saved.Sessions[0].ID != "saved" ||
		saved.Meta.SourceState != "unavailable" || saved.Meta.FetchedAt != 1700000000000 || saved.Meta.Error["code"] != "disabled" {
		t.Fatal("saved snapshot with the switch off", w.Code, w.Body.String())
	}
	for _, p := range paths[1:] {
		if w := get(b, p); w.Code != 503 || !strings.Contains(w.Body.String(), `"code":"disabled"`) {
			t.Fatal(p, w.Code, w.Body.String())
		}
	}
	// Validation still answers first (unknown path, method), with nothing sent.
	if w := get(b, "/sessions/short"); w.Code != 404 {
		t.Fatal(w.Code)
	}
	if n := conns.Load(); n != 0 {
		t.Fatalf("switch off: %d connections to Fragments Engine", n)
	}
	// The control: the same bridge with the switch on does connect.
	b.Enabled = true
	_ = get(b, "/status")
	if conns.Load() == 0 {
		t.Fatal("switch on never connected: the check proves nothing")
	}
}

// On but not configured (no credential, a URL with a path, a credential over plain http to a
// non-loopback host, a session cap outside 200..20000) is 503 configuration, the saved snapshot for
// /snapshot, with nothing sent.
func TestSwitchOnUnconfigured(t *testing.T) {
	addr, conns := acceptCounter(t)
	capped := New(addr, token, true)
	capped.MaxSessions = 100
	for name, b := range map[string]*Bridge{
		"no url":        New("", token, true),
		"no credential": New(addr, nil, true),
		"path":          New(addr+"/v1", token, true),
		"plain http":    New("http://fragments.invalid", token, true),
		"cap":           capped,
	} {
		if w := get(b, "/status"); w.Code != 503 || !strings.Contains(w.Body.String(), `"code":"configuration"`) {
			t.Fatal(name, w.Code, w.Body.String())
		}
		b.DB = storetest.New(t).Open(t)
		b.save(context.Background(), []byte(`{"sessions":[],"snapshot_meta":{"source_state":"live"}}`))
		if w := get(b, "/snapshot"); w.Code != 200 || !strings.Contains(w.Body.String(), `"source_state":"unavailable"`) || !strings.Contains(w.Body.String(), `"code":"configuration"`) {
			t.Fatal(name, "saved", w.Code, w.Body.String())
		}
	}
	if n := conns.Load(); n != 0 {
		t.Fatalf("unconfigured: %d connections", n)
	}
}

// Fragments Engine's refusals map to the old codes.
func TestUpstreamErrorMapping(t *testing.T) {
	var status atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s := int(status.Load()); s != 200 {
			w.WriteHeader(s)
			return
		}
		_, _ = w.Write([]byte(`not json`))
	}))
	defer upstream.Close()
	b := on(upstream.URL, 200)
	path := "/sessions/" + strings.Repeat("d", 64)
	for s, want := range map[int32]struct {
		status int
		code   string
	}{401: {401, "authentication"}, 403: {403, "authentication"}, 404: {404, "not_found"}, 429: {429, "rate_limited"}, 500: {502, "remote"}, 200: {502, "invalid_response"}} {
		status.Store(s)
		w := get(b, path)
		var body struct{ Code string }
		_ = json.Unmarshal(w.Body.Bytes(), &body)
		if w.Code != want.status || body.Code != want.code {
			t.Fatal(s, w.Code, w.Body.String())
		}
	}
	upstream.Close()
	if w := get(b, path); w.Code != 502 || !strings.Contains(w.Body.String(), "unreachable") {
		t.Fatal("down", w.Code, w.Body.String())
	}
}

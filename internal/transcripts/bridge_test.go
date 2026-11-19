package transcripts

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hollis-labs/tachyon/internal/apicache"
	"github.com/hollis-labs/tachyon/internal/localstore"
)

func token(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(path, []byte("test-fragments-token"), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}
func get(h http.Handler, path string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", Prefix+path, nil))
	return w
}
func TestSnapshotPaginationCoverageAndCredentialBoundary(t *testing.T) {
	cutoff := ""
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.Header.Get("Authorization") != "Bearer test-fragments-token" || r.Header.Get("Cookie") != "" {
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
	b := New(upstream.URL, token(t), 400)
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
	b := New(upstream.URL, token(t), 200)
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

func TestDurableSnapshotsSurviveOutageButNotDenialOrCredentialRemoval(t *testing.T) {
	remoteStatus := 200
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if remoteStatus != 200 {
			w.WriteHeader(remoteStatus)
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
	file := token(t)
	b := New(upstream.URL, file, 200)
	path := filepath.Join(t.TempDir(), "cache.db")
	open := func() *localstore.Store {
		db, e := localstore.Open(context.Background(), path)
		if e != nil {
			t.Fatal(e)
		}
		return db
	}
	db := open()
	settings := apicache.Defaults()
	p := settings.Policies["transcripts"]
	p.FreshSeconds = 0
	settings.Policies["transcripts"] = p
	wrap := func() http.Handler {
		return apicache.New(db, settings, func(string) (string, error) { return b.Identity() }).Wrap(b)
	}
	if w := get(wrap(), "/snapshot"); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db = open()
	defer db.Close()
	remoteStatus = 503
	if w := get(wrap(), "/snapshot"); w.Code != 200 || w.Header().Get("X-Tachyon-Cache") != "stale" {
		t.Fatal("durable fallback lost", w.Code, w.Body.String())
	}
	remoteStatus = 403
	if w := get(wrap(), "/snapshot"); w.Code != 403 {
		t.Fatal("denied access used old transcript data")
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if w := get(wrap(), "/snapshot"); w.Code != 503 || !strings.Contains(w.Body.String(), "configuration") {
		t.Fatal("missing credential concealed")
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
	b := New(upstream.URL, token(t), 200)
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
	for _, p := range []string{"/search", "/sessions/" + strings.Repeat("a", 64) + "/find", "/sessions/" + strings.Repeat("a", 64) + "/errors"} {
		if apicache.Scope(Prefix+p) != "" {
			t.Fatal("must not be cached:", p)
		}
	}
}

func TestStatusAddsCapabilities(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"available":true,"enabled":true}`))
	}))
	defer upstream.Close()
	w := get(New(upstream.URL, token(t), 200), "/status")
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
	db, err := localstore.Open(context.Background(), filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	b := New(upstream.URL, token(t), 200)
	b.DB = db
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
		if w.Code != 200 || saved.Meta.SourceState != "unavailable" || saved.Meta.Error == nil || len(saved.Sessions) != 2 || w.Header().Get(Uncacheable) == "" {
			t.Fatal(m, w.Code, w.Body.String())
		}
	}
	if w = get(New(upstream.URL, token(t), 200), "/snapshot"); w.Code != 403 {
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
	b := New(upstream.URL, token(t), 200)
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

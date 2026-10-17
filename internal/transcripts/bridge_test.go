package transcripts

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
	w := get(b, "/sessions/"+strings.Repeat("a", 64)+"?turn_offset=50&turn_limit=99999&raw=true")
	if w.Code != 502 || !touched {
		t.Fatal(w.Code, w.Body.String())
	}
	touched = false
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

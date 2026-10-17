package apicache

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hollis-labs/tachyon/internal/localstore"
)

const board = "/api/plugins/planner/v1/planner/board"

func openTest(t *testing.T, path string) *localstore.Store {
	t.Helper()
	s, e := localstore.Open(context.Background(), path)
	if e != nil {
		t.Fatal(e)
	}
	return s
}
func request(h http.Handler, method, path string, force bool) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, nil)
	if force {
		r.Header.Set("Cache-Control", "no-cache")
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func state(t *testing.T, w *httptest.ResponseRecorder, want string) {
	t.Helper()
	if got := w.Header().Get("X-Tachyon-Cache"); got != want {
		t.Fatalf("state %q != %q; status=%d body=%s", got, want, w.Code, w.Body.String())
	}
}
func jsonResponse(w http.ResponseWriter, b string) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(b))
}

func TestSnapshotSurvivesRestartAndOutage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.db")
	db := openTest(t, path)
	now := time.Now()
	ident := func(string) (string, error) { return "host", nil }
	c := New(db, Defaults(), ident)
	c.now = func() time.Time { return now }
	h := c.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { jsonResponse(w, `{"board":"saved"}`) }))
	live := request(h, "GET", board, false)
	state(t, live, "live")
	updated := live.Header().Get("X-Tachyon-Updated-At")
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db = openTest(t, path)
	defer db.Close()
	c = New(db, Defaults(), ident)
	c.now = func() time.Time { return now }
	h = c.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(502)
		_, _ = w.Write([]byte(`{"code":"unreachable"}`))
	}))
	state(t, request(h, "GET", board, false), "cached")
	now = now.Add(30 * time.Second)
	stale := request(h, "GET", board, false)
	state(t, stale, "stale")
	if stale.Body.String() != live.Body.String() || stale.Header().Get("X-Tachyon-Updated-At") != updated || stale.Header().Get("X-Tachyon-Cache-Error") != "unreachable" {
		t.Fatal("stale fallback lost data, original timestamp or connection error")
	}
	now = now.Add(25 * time.Hour)
	state(t, request(h, "GET", board, false), "error")
	h = c.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { jsonResponse(w, `{"board":"recovered"}`) }))
	recovered := request(h, "GET", board, false)
	state(t, recovered, "live")
	if recovered.Body.String() == live.Body.String() {
		t.Fatal("recovery did not replace stale data")
	}
}

func TestDeniedCredentialsAndChangedIdentityNeverUseOldSnapshot(t *testing.T) {
	db := openTest(t, filepath.Join(t.TempDir(), "app.db"))
	defer db.Close()
	id := "first"
	var identityErr error
	c := New(db, Defaults(), func(string) (string, error) { return id, identityErr })
	status := 200
	h := c.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	state(t, request(h, "GET", board, false), "live")
	status = 401
	denied := request(h, "GET", board, true)
	state(t, denied, "error")
	if denied.Code != 401 || denied.Header().Get("X-Tachyon-Cache-Error") != "authentication" {
		t.Fatal("denial was hidden")
	}
	status = 502
	state(t, request(h, "GET", board, false), "error") // denied entry was deleted
	status = 200
	state(t, request(h, "GET", board, false), "live")
	id = "second"
	status = 502
	state(t, request(h, "GET", board, false), "error")
	id = "first"
	identityErr = errors.New("credential missing")
	bad := request(h, "GET", board, false)
	state(t, bad, "error")
	if bad.Header().Get("X-Tachyon-Cache-Error") != "configuration" {
		t.Fatal("missing local credential not identified")
	}
}

func TestRefreshMutationAndDestructiveReads(t *testing.T) {
	db := openTest(t, filepath.Join(t.TempDir(), "app.db"))
	defer db.Close()
	c := New(db, Defaults(), func(string) (string, error) { return "host", nil })
	var remote atomic.Int32
	h := c.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { remote.Add(1); jsonResponse(w, `{"ok":true}`) }))
	state(t, request(h, "GET", board, false), "live")
	state(t, request(h, "GET", board, false), "cached")
	state(t, request(h, "GET", board, true), "live")
	request(h, "POST", board, false)
	state(t, request(h, "GET", board, false), "live")
	if remote.Load() != 4 {
		t.Fatal("cache hit, explicit refresh or mutation invalidation incorrect")
	}
	for _, path := range []string{"/api/plugins/console/v1/messages/inbox", "/api/plugins/console/v1/broker/inbox", "/api/plugins/console/stream/activity"} {
		before := remote.Load()
		for range 2 {
			w := request(h, "GET", path, false)
			if w.Header().Get("X-Tachyon-Cache") != "" {
				t.Fatalf("cached unsafe read %s", path)
			}
		}
		if remote.Load() != before+2 {
			t.Fatal("unsafe reads must reach their handler")
		}
	}
}

func TestConcurrentMissesAndQueryIsolation(t *testing.T) {
	db := openTest(t, filepath.Join(t.TempDir(), "app.db"))
	defer db.Close()
	c := New(db, Defaults(), func(string) (string, error) { return "host", nil })
	var remote atomic.Int32
	h := c.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { remote.Add(1); jsonResponse(w, `{"ok":true}`) }))
	var wg sync.WaitGroup
	for range 12 {
		wg.Go(func() { request(h, "GET", board+"?scope=a", false) })
	}
	wg.Wait()
	if remote.Load() != 1 {
		t.Fatal("concurrent misses were not coalesced")
	}
	state(t, request(h, "GET", board+"?scope=b", false), "live")
	state(t, request(h, "GET", board+"?scope=a", false), "cached")
}

func TestPolicyPersistsAndEvictsOldest(t *testing.T) {
	dir := t.TempDir()
	db := openTest(t, filepath.Join(dir, "app.db"))
	defer db.Close()
	file := filepath.Join(dir, "policy.json")
	if err := os.WriteFile(file, []byte(`{"max_entries":1,"policies":{"planner":{"fresh_seconds":30}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadSettings(context.Background(), db, file)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err = LoadSettings(context.Background(), db, "")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Policies["planner"].FreshSeconds != 30 || cfg.Policies["planner"].TimeoutSeconds != 15 {
		t.Fatal("partial policy did not preserve defaults or persist")
	}
	now := time.Now()
	c := New(db, cfg, func(string) (string, error) { return "host", nil })
	c.now = func() time.Time { return now }
	h := c.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { jsonResponse(w, `{"ok":true}`) }))
	state(t, request(h, "GET", board+"?scope=a", false), "live")
	now = now.Add(time.Second)
	state(t, request(h, "GET", board+"?scope=b", false), "live")
	state(t, request(h, "GET", board+"?scope=b", false), "cached")
	state(t, request(h, "GET", board+"?scope=a", false), "live")
	if err = os.WriteFile(file, []byte(`{"policies":{"planner":{"timeout_seconds":0}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = LoadSettings(context.Background(), db, file); err == nil {
		t.Fatal("invalid knob accepted")
	}
}

func TestTimeoutAndMalformedResponses(t *testing.T) {
	db := openTest(t, filepath.Join(t.TempDir(), "app.db"))
	defer db.Close()
	cfg := Defaults()
	p := cfg.Policies["planner"]
	p.TimeoutSeconds = 1
	cfg.Policies["planner"] = p
	c := New(db, cfg, func(string) (string, error) { return "host", nil })
	h := c.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	w := request(h, "GET", board, false)
	state(t, w, "error")
	if w.Header().Get("X-Tachyon-Cache-Error") != "timeout" {
		t.Fatal("timeout classified incorrectly")
	}
	h = c.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { jsonResponse(w, `not json`) }))
	w = request(h, "GET", board, false)
	state(t, w, "error")
	if w.Header().Get("X-Tachyon-Cache-Error") != "invalid_response" {
		t.Fatal("invalid response treated as live")
	}
}

func TestDeniedIdentityInvalidatesOtherResources(t *testing.T) {
	db := openTest(t, filepath.Join(t.TempDir(), "app.db"))
	defer db.Close()
	c := New(db, Defaults(), func(string) (string, error) { return "credential", nil })
	status := 200
	h := c.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	state(t, request(h, "GET", board+"?scope=a", false), "live")
	state(t, request(h, "GET", board+"?scope=b", false), "live")
	status = 403
	state(t, request(h, "GET", board+"?scope=a", true), "error")
	status = 502
	state(t, request(h, "GET", board+"?scope=b", false), "error")
}

func TestCredentialRotationDuringRequestDoesNotPersistResponse(t *testing.T) {
	db := openTest(t, filepath.Join(t.TempDir(), "app.db"))
	defer db.Close()
	identity := "before"
	c := New(db, Defaults(), func(string) (string, error) { return identity, nil })
	h := c.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { identity = "after"; jsonResponse(w, `{"private":true}`) }))
	state(t, request(h, "GET", board, false), "error")
	identity = "before"
	h = c.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(502) }))
	state(t, request(h, "GET", board, false), "error")
}

func TestNoStoreAndByteBudget(t *testing.T) {
	db := openTest(t, filepath.Join(t.TempDir(), "app.db"))
	defer db.Close()
	cfg := Defaults()
	cfg.MaxBytes = 1024
	c := New(db, cfg, func(string) (string, error) { return "credential", nil })
	body := `{"ok":true}`
	h := c.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { jsonResponse(w, body) }))
	r := httptest.NewRequest("GET", board, nil)
	r.Header.Set("Cache-Control", "no-store")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	state(t, w, "live")
	// A no-store read must not create a durable cache hit.
	state(t, request(h, "GET", board, false), "live")
	body = `{"body":"` + strings.Repeat("x", 2000) + `"}`
	oversized := request(h, "GET", board+"?scope=large", false)
	state(t, oversized, "live")
	if oversized.Header().Get("X-Tachyon-Cache-Error") != "oversized" {
		t.Fatal("oversized snapshot not reported")
	}
	h2 := c.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(502) }))
	state(t, request(h2, "GET", board+"?scope=large", false), "error")
	body = `{"body":"` + string(make([]byte, 2000)) + `"}` // invalid JSON must not replace the good snapshot
	w = request(h, "GET", board, true)
	state(t, w, "stale")
}

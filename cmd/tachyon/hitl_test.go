package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/tachyon/internal/contract"
	"github.com/hollis-labs/tachyon/internal/hitl"
)

func TestHITLHTTPAskAndOrigin(t *testing.T) {
	root, mgr, logger := startupFixture(t)
	dir := fixturePlugin(t, root, "asker", `{"modules":["work"],"verbs":{"work_write":{"effect":"writes"}}}`, true)
	if e := os.WriteFile(filepath.Join(dir, "verb-result"), []byte(`{"status":"ask","ask":{"prompt":"Proceed?","options":["yes"],"context":{"keep":true}}}`), 0600); e != nil {
		t.Fatal(e)
	}
	if e := loadStartupPlugins(context.Background(), mgr, root, logger); e != nil {
		t.Fatal(e)
	}
	runtime, _ := hitl.NewRuntime(nil, "")
	mux := http.NewServeMux()
	registerHITL(mux, mgr, runtime, logger)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "http://localhost/api/verb/work_write", strings.NewReader(`{"amount":1}`))
	r.ContentLength = -1
	mux.ServeHTTP(w, r)
	var env contract.ResultEnvelope
	_ = json.Unmarshal(w.Body.Bytes(), &env)
	if w.Code != 200 || env.Status != "ask" || env.Ask == nil || env.Ask.Unavailable != "not_configured" || env.Ask.Continuation != "unavailable" || env.Ask.Context["keep"] != true {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, origin := range []string{"https://evil.test", "null", "http://localhost.evil"} {
		w = httptest.NewRecorder()
		r = httptest.NewRequest("GET", "http://localhost/api/hitl/operations/id", nil)
		r.Header.Set("Origin", origin)
		mux.ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatal(w.Code)
		}
	}
	for _, site := range []string{"same-site", "cross-site"} {
		w = httptest.NewRecorder()
		r = httptest.NewRequest("GET", "http://localhost/api/hitl/operations/id", nil)
		r.Header.Set("Sec-Fetch-Site", site)
		mux.ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatal(site, w.Code)
		}
	}
	w = httptest.NewRecorder()
	r = httptest.NewRequest("GET", "http://localhost/api/hitl/operations/id", nil)
	r.Header.Set("Origin", "http://localhost")
	mux.ServeHTTP(w, r)
	if w.Code != 410 || !strings.Contains(w.Body.String(), `"approved":false`) || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("POST", "http://localhost/api/hitl/operations/id", nil))
	if w.Code != 405 {
		t.Fatal("continuation POST exposed", w.Code)
	}
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "http://localhost/api/hitl/operations/id?wait_ms=50001", nil))
	if w.Code != 400 {
		t.Fatal(w.Code)
	}
}

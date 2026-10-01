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

	"github.com/hollis-labs/tachyon/internal/hitl"
	"github.com/hollis-labs/tachyon/internal/observefeed"
)

func TestObservePrivateCommandHTTPAndRegistryGuards(t *testing.T) {
	root, mgr, logger := startupFixture(t)
	dir := fixturePlugin(t, root, "observe-ops", `{"modules":["observe"],"verbs":{"observe_events":{"effect":"reads"}}}`, true)
	if err := os.WriteFile(filepath.Join(dir, "verb-result"), []byte(`{"status":"ok","data":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := loadStartupPlugins(context.Background(), mgr, root, logger); err != nil {
		t.Fatal(err)
	}
	bridge, _ := hitl.NewRuntime(nil, "")
	mux := http.NewServeMux()
	registerHITL(mux, mgr, bridge, logger)
	proxy := &pluginProxy{mgr: mgr, pluginID: "observe-ops", logger: logger}
	// Even a mistakenly registered generic command handler cannot expose ingestion.
	mux.HandleFunc("POST /api/observe/commands", proxy.command(observefeed.Command, func(r *http.Request) (string, error) { t.Error("private HTTP body was read"); return "", nil }))
	mux.HandleFunc("GET /api/verbs", func(w http.ResponseWriter, r *http.Request) { _ = json.NewEncoder(w).Encode(mgr.AllCapabilities()) })
	mux.HandleFunc("GET /api/plugins/registry", func(w http.ResponseWriter, r *http.Request) { _ = json.NewEncoder(w).Encode(mgr.BuildRegistry()) })
	for _, tc := range []struct {
		path   string
		status int
		code   string
	}{{"/api/verb/" + observefeed.Command, 404, "unknown_verb"}, {"/api/observe/commands", 403, "private host command"}} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("POST", "http://localhost"+tc.path, strings.NewReader(`{"token":"forged","records":[{"payload":"INJECTED-SECRET"}]}`)))
		if w.Code != tc.status || !strings.Contains(w.Body.String(), tc.code) {
			t.Fatal(tc.path, w.Code, w.Body.String())
		}
	}
	for _, path := range []string{"/api/verbs", "/api/plugins/registry"} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("GET", "http://localhost"+path, nil))
		if w.Code != 200 || strings.Contains(w.Body.String(), observefeed.Command) || strings.Contains(w.Body.String(), observefeed.TokenConfig) {
			t.Fatal("private command/marker advertised", w.Body.String())
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "verb-payload")); !os.IsNotExist(err) {
		t.Fatal("HTTP forged command reached plugin", err)
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("POST", "http://localhost/api/verb/observe_events", strings.NewReader(`{}`)))
	if w.Code != 200 || strings.Contains(w.Body.String(), "INJECTED-SECRET") {
		t.Fatal("HTTP forgery entered Observe", w.Code, w.Body.String())
	}
}

package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAlertsStubIsMountedOnlyWhenEnabled(t *testing.T) {
	off := New(Config{PluginBinDir: t.TempDir()}).Routes()
	t.Setenv("TACHYON_ALERTS_STUBS", "1")
	on := New(Config{PluginBinDir: t.TempDir()}).Routes()
	base := "https://tachyon.test/api/plugins/console/v1"
	for _, p := range []string{"/alerts", "/alerts/counts", "/alerts/al_approval_01", "/console/state"} {
		w := httptest.NewRecorder()
		on.ServeHTTP(w, httptest.NewRequest("GET", base+p, nil))
		if w.Code != 200 || w.Header().Get("X-Tachyon-Alerts-Stub") != "1" {
			t.Errorf("GET %s with the flag on: %d", p, w.Code)
		}
		w = httptest.NewRecorder()
		off.ServeHTTP(w, httptest.NewRequest("GET", base+p, nil))
		if w.Header().Get("X-Tachyon-Alerts-Stub") != "" {
			t.Errorf("GET %s served a stub with the flag off", p)
		}
	}
	for _, c := range []struct{ method, path string }{{"POST", "/alerts/al_approval_01/ops"}, {"POST", "/alerts/ops/bulk"}, {"PUT", "/alerts/prefs"}, {"PUT", "/console/state/al_approval_01"}} {
		w := httptest.NewRecorder()
		on.ServeHTTP(w, httptest.NewRequest(c.method, base+c.path, strings.NewReader(`{}`)))
		if w.Code != 403 {
			t.Errorf("%s %s without a same-origin Origin: %d", c.method, c.path, w.Code)
		}
	}
}

func TestAlertsRealRoutes(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
		w.Write([]byte(`{"elevated":false}`))
	}))
	defer upstream.Close()
	ctx, cancel := context.WithCancel(context.Background())
	s := New(Config{PluginBinDir: t.TempDir(), DataDir: t.TempDir(), CacheDir: t.TempDir(), ApprovalQueueURL: upstream.URL, TranscriptMaxSessions: 5000})
	if err := s.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() { cancel(); _ = s.Stop(context.Background()) }()
	h := s.Routes()
	base := "https://tachyon.test/api/plugins/console/v1"
	do := func(method, path, body string, origin bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, base+path, strings.NewReader(body))
		if origin {
			r.Header.Set("Origin", "https://tachyon.test")
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	for _, p := range []string{"/alerts", "/alerts/counts", "/alerts/prefs"} {
		if w := do("GET", p, "", false); w.Code != 200 || w.Header().Get("X-Tachyon-Alerts-Stub") != "" {
			t.Errorf("GET %s: %d %s", p, w.Code, w.Body)
		}
	}
	if w := do("POST", "/alerts/ops/bulk", `{"op":"read","scope":"all_active"}`, false); w.Code != 403 {
		t.Errorf("bulk without Origin: %d", w.Code)
	}
	if w := do("POST", "/alerts/ops/bulk", `{"op":"read","scope":"all_active"}`, true); w.Code != 200 {
		t.Errorf("bulk from the page: %d %s", w.Code, w.Body)
	}
	// /console/state: alert keys are served by the alert store (no operator elevation), others keep the
	// operator guard.
	if w := do("PUT", "/console/state/al_missing", `{"type":"read"}`, true); w.Code != 404 {
		t.Errorf("alert key: %d %s", w.Code, w.Body)
	}
	if w := do("PUT", "/console/state/pr-inbox:a", `{"type":"read"}`, true); w.Code != 401 && w.Code != 403 {
		t.Errorf("other key without an operator session: %d", w.Code)
	}
	if w := do("GET", "/console/state", "", false); w.Code != 200 || !strings.Contains(w.Body.String(), `"data"`) {
		t.Errorf("state list: %d %s", w.Code, w.Body)
	}
}

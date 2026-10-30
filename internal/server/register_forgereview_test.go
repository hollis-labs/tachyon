package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestForgeReviewRoutesAreMountedAndGuarded(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(403) }))
	defer upstream.Close()
	h := New(Config{PluginBinDir: t.TempDir(), ApprovalQueueURL: upstream.URL}).Routes()
	base := "https://tachyon.test/api/plugins/console/v1/forge/repos/github/o/r/pulls/7"
	do := func(method, path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(method, base+path, strings.NewReader(`{}`)))
		return w
	}
	for _, c := range []struct{ method, path string }{{"POST", "/reviews"}, {"POST", "/reviews/validate"}, {"PUT", "/review-draft"}, {"DELETE", "/review-draft"}} {
		if w := do(c.method, c.path); w.Code != 401 && w.Code != 403 {
			t.Errorf("%s %s without an operator session: %d %s", c.method, c.path, w.Code, w.Body)
		}
	}
	// The draft read is a read: it reaches the handler (no local store in this test, so it says so).
	if w := do("GET", "/review-draft"); w.Code != 503 {
		t.Errorf("draft read: %d %s", w.Code, w.Body)
	}
}

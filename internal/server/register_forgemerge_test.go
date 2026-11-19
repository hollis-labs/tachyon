package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestForgeMergeRoutesAreMountedAndGuarded(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(403) }))
	defer upstream.Close()
	h := New(Config{PluginBinDir: t.TempDir(), ApprovalQueueURL: upstream.URL}).Routes()
	base := "https://tachyon.test/api/plugins/console/v1/forge/repos/github/o/r/pulls/7"
	do := func(method, path, body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(method, base+path, strings.NewReader(body)))
		return w
	}
	// No operator session: the merge is refused before it can reach GitHub.
	if w := do("PUT", "/merge", `{"method":"squash","head_sha":"`+strings.Repeat("a", 40)+`"}`); w.Code != 401 && w.Code != 403 {
		t.Errorf("merge without an operator session: %d %s", w.Code, w.Body)
	}
	// Preflight is a read: it reaches the handler, which has no GitHub token here.
	if w := do("GET", "/merge-preflight", ""); w.Code != 401 || !strings.Contains(w.Body.String(), "credential_missing") {
		t.Errorf("preflight: %d %s", w.Code, w.Body)
	}
}

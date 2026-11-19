package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestApprovalsStubRoutesRequireOperatorAndKeepTheRealSession(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
		w.Write([]byte(`{"elevated":false}`))
	}))
	defer upstream.Close()
	off := New(Config{PluginBinDir: t.TempDir(), ApprovalQueueURL: upstream.URL}).Routes()
	t.Setenv("TACHYON_APPROVALS_STUBS", "1")
	on := New(Config{PluginBinDir: t.TempDir(), ApprovalQueueURL: upstream.URL}).Routes()
	const p = "/api/plugins/console/v1/approval-inbox"
	cases := []struct{ method, path string }{
		{"POST", p + "/items/ap_change_a/resolve"},
		{"POST", p + "/items/ap_change_a/defer"},
		{"DELETE", p + "/items/ap_deferred/defer"},
		{"POST", p + "/batch/resolve"},
		{"POST", p + "/secrets"},
	}
	for _, c := range cases {
		req := httptest.NewRequest(c.method, "https://tachyon.test"+c.path, strings.NewReader(`{}`))
		req.Header.Set("Origin", "https://tachyon.test")
		w := httptest.NewRecorder()
		on.ServeHTTP(w, req)
		if (w.Code != 401 && w.Code != 403) || w.Header().Get("X-Tachyon-Approvals-Stub") != "" {
			t.Errorf("%s %s with stubs on and no operator session: %d", c.method, c.path, w.Code)
		}
		w = httptest.NewRecorder()
		off.ServeHTTP(w, httptest.NewRequest(c.method, "https://tachyon.test"+c.path, strings.NewReader(`{}`)))
		if w.Header().Get("X-Tachyon-Approvals-Stub") != "" {
			t.Errorf("%s %s served a stub with the flag off", c.method, c.path)
		}
	}
	w := httptest.NewRecorder()
	on.ServeHTTP(w, httptest.NewRequest("GET", "https://tachyon.test"+p+"/inbox", nil))
	if w.Code != 200 || w.Header().Get("X-Tachyon-Approvals-Stub") != "1" {
		t.Errorf("stub inbox: %d", w.Code)
	}
	// The real bridge still owns the session routes.
	w = httptest.NewRecorder()
	on.ServeHTTP(w, httptest.NewRequest("GET", "https://tachyon.test"+p+"/status", nil))
	if w.Header().Get("X-Tachyon-Approvals-Stub") != "" {
		t.Error("status must stay with the real bridge")
	}
}

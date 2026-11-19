package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestForgeStubRoutesRequireOperator(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
		w.Write([]byte(`{"elevated":false}`))
	}))
	defer upstream.Close()
	off := New(Config{PluginBinDir: t.TempDir(), ApprovalQueueURL: upstream.URL}).Routes()
	t.Setenv("TACHYON_FORGE_STUBS", "1")
	on := New(Config{PluginBinDir: t.TempDir(), ApprovalQueueURL: upstream.URL}).Routes()
	f := "/api/plugins/console/v1/forge"
	cases := []struct{ method, path string }{
		{"PUT", f + "/repos/github/hollis-labs/tachyon/pulls/202/merge"},
		{"POST", f + "/repos/github/hollis-labs/tachyon/pulls/202/reviews"},
		{"POST", f + "/repos/github/hollis-labs/tachyon/pulls/202/assign-agent"},
		{"PUT", "/api/plugins/console/v1/console/state/pr-inbox:github:hollis-labs%2Ftachyon%23201"},
		{"POST", "/api/plugins/console/v1/console/state/bulk"},
	}
	for _, c := range cases {
		r := httptest.NewRequest(c.method, "https://tachyon.test"+c.path, strings.NewReader(`{}`))
		r.Header.Set("Origin", "https://tachyon.test")
		w := httptest.NewRecorder()
		on.ServeHTTP(w, r)
		if (w.Code != 401 && w.Code != 403) || w.Header().Get("X-Tachyon-Forge-Stub") != "" {
			t.Errorf("%s %s with stubs on and no operator session: %d", c.method, c.path, w.Code)
		}
		w = httptest.NewRecorder()
		off.ServeHTTP(w, httptest.NewRequest(c.method, "https://tachyon.test"+c.path, strings.NewReader(`{}`)))
		if w.Header().Get("X-Tachyon-Forge-Stub") != "" {
			t.Errorf("%s %s served a stub with the flag off", c.method, c.path)
		}
	}
	w := httptest.NewRecorder()
	on.ServeHTTP(w, httptest.NewRequest("GET", "https://tachyon.test"+f+"/pulls/pending", nil))
	if w.Code != 200 || w.Header().Get("X-Tachyon-Forge-Stub") != "1" {
		t.Errorf("stub list: %d", w.Code)
	}
}

func TestPluginForgeWritesNeedAnOperatorSession(t *testing.T) {
	h := New(Config{PluginBinDir: t.TempDir()}).Routes()
	base := "https://tachyon.test/api/plugins/console/v1/forge/repos/github/o/r/pulls/1"
	for _, c := range [][2]string{{"POST", "/comments"}, {"PUT", "/assignees"}, {"PUT", "/merge"}, {"POST", "/reviews"}} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(c[0], base+c[1], strings.NewReader(`{"body":"x"}`)))
		if w.Code != 403 {
			t.Errorf("%s %s without a session: %d %s", c[0], c[1], w.Code, w.Body.String())
		}
	}
}

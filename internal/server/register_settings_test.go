package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSettingsStubIsMountedOnlyWhenEnabled(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(403) }))
	defer upstream.Close()
	off := New(Config{PluginBinDir: t.TempDir(), ApprovalQueueURL: upstream.URL}).Routes()
	t.Setenv("TACHYON_SETTINGS_STUBS", "1")
	on := New(Config{PluginBinDir: t.TempDir(), ApprovalQueueURL: upstream.URL}).Routes()
	const p = "https://tachyon.test/api/plugins/console/v1"
	for _, path := range []string{"/admin/manifest", "/account"} {
		w := httptest.NewRecorder()
		on.ServeHTTP(w, httptest.NewRequest("GET", p+path, nil))
		if w.Code != 200 || w.Header().Get("X-Tachyon-Settings-Stub") != "1" {
			t.Errorf("stub %s: %d", path, w.Code)
		}
		w = httptest.NewRecorder()
		off.ServeHTTP(w, httptest.NewRequest("GET", p+path, nil))
		if w.Header().Get("X-Tachyon-Settings-Stub") != "" {
			t.Errorf("%s served a stub with the flag off", path)
		}
	}
}

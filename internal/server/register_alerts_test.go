package server

import (
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

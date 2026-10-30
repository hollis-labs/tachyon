package server

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hollis-labs/tachyon/internal/settingsapi"
)

func TestSettingsRegistryRoutesAreMountedForReal(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	s := New(Config{PluginBinDir: t.TempDir(), DataDir: t.TempDir(), CacheDir: t.TempDir(), TranscriptMaxSessions: 5000})
	if err := s.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() { cancel(); _ = s.Stop(context.Background()) }()
	if settingsRegistry(s) == nil {
		t.Fatal("registry missing")
	}
	h := s.Routes()
	for _, c := range []struct{ method, path string }{
		{"GET", "/manifest"}, {"GET", "/settings/general"}, {"POST", "/settings/general/update"}, {"POST", "/settings/general/reset"}, {"POST", "/settings/general/validate"},
	} {
		r := httptest.NewRequest(c.method, "https://tachyon.test"+settingsapi.AdminPrefix+c.path, strings.NewReader(`{}`))
		r.Header.Set("Origin", "https://tachyon.test")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Header().Get("X-Tachyon-Settings-Stub") != "" || w.Code == 404 || w.Code == 200 {
			t.Errorf("%s %s: %d (no session must be refused by the registry handler, not stubbed or open)", c.method, c.path, w.Code)
		}
	}
}

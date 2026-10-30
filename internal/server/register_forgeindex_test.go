package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestForgeIndexRoutes(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(403) }))
	defer upstream.Close()
	ctx, cancel := context.WithCancel(context.Background())
	s := New(Config{PluginBinDir: t.TempDir(), DataDir: t.TempDir(), CacheDir: t.TempDir(), ApprovalQueueURL: upstream.URL, TranscriptMaxSessions: 5000})
	if err := s.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() { cancel(); _ = s.Stop(context.Background()) }()
	h := s.Routes()
	base := "https://tachyon.test/api/plugins/console/v1/forge"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", base+"/status", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"watched"`) {
		t.Errorf("status: %d %s", w.Code, w.Body)
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", base+"/stream?once=1", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"type":"status"`) {
		t.Errorf("stream: %d %s", w.Code, w.Body)
	}
	// Writes are not served by the index.
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("POST", base+"/status", nil))
	if w.Header().Get("Content-Type") == "application/json" && strings.Contains(w.Body.String(), `"watched"`) {
		t.Error("the index must not answer writes")
	}
}

func TestForgeIndexOffWithStubsOrFlag(t *testing.T) {
	t.Setenv("TACHYON_FORGE_INDEX", "0")
	s := New(Config{PluginBinDir: t.TempDir(), DataDir: t.TempDir(), CacheDir: t.TempDir(), TranscriptMaxSessions: 5000})
	ctx, cancel := context.WithCancel(context.Background())
	if err := s.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() { cancel(); _ = s.Stop(context.Background()) }()
	if _, ok := s.ext.Load(forgeIndexKey); ok {
		t.Error("TACHYON_FORGE_INDEX=0 must not start the index")
	}
}

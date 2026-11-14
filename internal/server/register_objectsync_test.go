package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/tachyon/internal/catalog"
)

// With Tether configured but no schedule enabled (the state live has been in since 2026-10-30), the status route
// reports every Tether source as stale and the watchdog raises one sync.stalled warning per source, once.
func TestObjectSyncStatusAndStallAlert(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(404) }))
	defer upstream.Close()
	token := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(token, []byte("t"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := New(Config{PluginBinDir: t.TempDir(), DataDir: t.TempDir(), CacheDir: t.TempDir(), ApprovalQueueURL: upstream.URL, TranscriptMaxSessions: 5000,
		Upstreams: catalog.Upstreams{TetherAddr: upstream.URL, TetherTokenFile: token}})
	if err := s.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() { cancel(); _ = s.Stop(context.Background()) }()
	h := s.Routes()

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "https://tachyon.test"+ObjectSyncStatusPath, nil))
	if w.Code != 200 {
		t.Fatalf("status: %d %s", w.Code, w.Body)
	}
	var body struct {
		Sources []struct {
			Name   string `json:"name"`
			Stale  bool   `json:"stale"`
			Reason string `json:"reason"`
		} `json:"sources"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, src := range body.Sources {
		names[src.Name] = true
		if !src.Stale || !strings.Contains(src.Reason, "--enable") {
			t.Errorf("%s should be stale with an enable hint: %+v", src.Name, src)
		}
	}
	for _, want := range []string{"tether-projects", "tether-agents", "tether-workstreams", "tether-sessions"} {
		if !names[want] {
			t.Errorf("missing %s in %v", want, names)
		}
	}

	alerts := func() int {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", "https://tachyon.test/api/plugins/console/v1/alerts?limit=200", nil))
		return strings.Count(w.Body.String(), `"sync.stalled"`)
	}
	s.checkObjectSync(ctx, time.Now())
	first := alerts()
	if first < 4 {
		t.Fatalf("want a sync.stalled alert per Tether source, got %d", first)
	}
	s.checkObjectSync(ctx, time.Now().Add(time.Minute))
	if again := alerts(); again != first {
		t.Fatalf("a second check of the same stall must not add alerts: %d then %d", first, again)
	}
}

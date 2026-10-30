package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestConsoleStateRoutes(t *testing.T) {
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
	base := "https://tachyon.test/api/plugins/console/v1/console/state"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", base+"?prefix=pr-inbox:", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"data":[]`) {
		t.Errorf("open read: %d %s", w.Code, w.Body)
	}
	for _, c := range []struct{ method, path string }{
		{"PUT", base + "/pr-inbox:github:hollis-labs%2Ftachyon%23201"},
		{"PUT", base + "/pr-inbox:a"},
		{"POST", base + "/bulk"},
	} {
		r := httptest.NewRequest(c.method, c.path, strings.NewReader(`{"type":"read"}`))
		r.Header.Set("Origin", "https://tachyon.test")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 401 && w.Code != 403 {
			t.Errorf("%s %s without an operator session: %d", c.method, c.path, w.Code)
		}
	}
}

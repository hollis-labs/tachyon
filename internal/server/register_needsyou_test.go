package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/tachyon/internal/catalog"
	"github.com/hollis-labs/tachyon/internal/needsyou"
)

// GET needs-you is mounted and reads Tether live: a typed ask waiting on @chrispian is listed.
func TestNeedsYouRouteReadsTetherLive(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/messages/pending-replies":
			if r.URL.Query().Get("as") != needsyou.Operator {
				t.Errorf("pending replies asked as %q", r.URL.Query().Get("as"))
			}
			fmt.Fprint(w, `{"handle":"@chrispian","pending":[{"message_id":"m1","from":"msg://agent/agent-mux/agt_b","kind":"decision","subject":"Keep or patch?","created_at":"2026-11-14T10:00:00Z"}]}`)
		case "/messages/list":
			fmt.Fprint(w, `{"messages":[],"total":0}`)
		default:
			w.WriteHeader(404)
		}
	}))
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
	w := httptest.NewRecorder()
	s.Routes().ServeHTTP(w, httptest.NewRequest("GET", "https://tachyon.test"+needsyou.Path, nil))
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	var r needsyou.Result
	if err := json.Unmarshal(w.Body.Bytes(), &r); err != nil {
		t.Fatal(err)
	}
	if len(r.Items) != 1 || r.Items[0].ID != "ask:m1" || r.Items[0].Title != "Keep or patch?" || !r.Sources["asks"].OK || !r.Sources["mail"].OK {
		t.Fatalf("%+v", r)
	}
}

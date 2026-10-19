package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hollis-labs/tachyon/internal/catalog"
)

func TestActivityStreamDirectAndCredentialIsolated(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/stream" || r.URL.Query().Get("since") != "19" || r.URL.Query().Get("kind") != "agent" || r.URL.Query().Get("secret") != "" {
			t.Errorf("bad stream request: %s", r.URL)
		}
		if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" || r.Header.Get("X-Private") != "" || r.Header.Get("Last-Event-ID") != "18" {
			t.Error("credential leak or resume lost")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "id: 20\nevent: activity\ndata: {}\n\n")
	}))
	defer up.Close()
	s := &Server{cfg: Config{Upstreams: catalog.Upstreams{ActivityURL: up.URL, TorqueURL: "http://127.0.0.1:1", TorqueToken: "torque-secret"}}}
	for _, e := range catalog.Entries() {
		if e.ID == "console" {
			s.states = append(s.states, pluginState{entry: e, running: true})
		}
	}
	r := httptest.NewRequest(http.MethodGet, "/api/plugins/console/stream/activity?since=19&kind=agent&secret=drop", nil)
	r.SetPathValue("id", "console")
	r.SetPathValue("name", "activity")
	r.Header.Set("Authorization", "Bearer browser-secret")
	r.Header.Set("Cookie", "private=secret")
	r.Header.Set("X-Private", "secret")
	r.Header.Set("Last-Event-ID", "18")
	w := httptest.NewRecorder()
	s.stream(w, r)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "id: 20") {
		t.Fatal(w.Code, w.Body.String())
	}
	before, _ := s.cacheIdentity("activity")
	s.cfg.Upstreams.ActivityURL = "http://another-monitor"
	after, _ := s.cacheIdentity("activity")
	if before == after {
		t.Fatal("activity cache crosses monitors")
	}
}

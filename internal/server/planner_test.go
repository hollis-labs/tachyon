package server

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hollis-labs/tachyon/internal/approvals"
	"github.com/hollis-labs/tachyon/internal/catalog"
	"github.com/hollis-labs/tachyon/internal/localstore"
	"github.com/hollis-labs/tachyon/internal/objects"
	"github.com/hollis-labs/tachyon/internal/planner"
)

func TestOwnedPlannerRoutesEnforceOperatorAndBypassPlugin(t *testing.T) {
	elevated := false
	auth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"elevated": elevated, "assurance": "operator-token"})
	}))
	defer auth.Close()
	db, err := localstore.Open(context.Background(), filepath.Join(t.TempDir(), "tachyon.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := New(Config{PluginBinDir: t.TempDir(), ApprovalQueueURL: auth.URL})
	s.store = db
	s.objects = objects.New(db)
	handler := s.Routes()
	id := uuid.NewString()
	body := `{"id":"` + id + `","fields":{"title":"Authorized"}}`
	call := func(method, path, body, origin string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "https://tachyon.test"+path, strings.NewReader(body))
		r.Header.Set("Origin", origin)
		r.AddCookie(&http.Cookie{Name: approvals.CookieName, Value: "operator-session"})
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	if w := call("POST", planner.Prefix+"/ideas", body, "https://tachyon.test"); w.Code != 403 {
		t.Fatal(w.Code, w.Body.String())
	}
	if _, err := s.objects.Get(context.Background(), id); err != objects.ErrNotFound {
		t.Fatal("unauthorized record created", err)
	}
	elevated = true
	if w := call("POST", planner.Prefix+"/ideas", body, "https://elsewhere.test"); w.Code != 403 {
		t.Fatal(w.Code)
	}
	if w := call("POST", planner.Prefix+"/ideas", body, "https://tachyon.test"); w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, prefix := range []string{planner.Prefix, planner.LegacyPrefix} {
		if w := call("GET", prefix+"/ideas/"+id, "", ""); w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	history, err := s.objects.History(context.Background(), id, 0, 10)
	if err != nil || history[0].Actor != "operator:browser" {
		t.Fatal(history, err)
	}
}

// Exercise the browser transport against an isolated database. Only the
// external assurance service is a fixture; routing, guards, service writes,
// history, conflict responses and SSE all run through the real host.
func TestOwnedPlannerHTTPWorkflow(t *testing.T) {
	var elevated atomic.Bool
	elevated.Store(true)
	auth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie("tangent_participant")
		if r.URL.Path != "/api/session" || err != nil || cookie.Value != "isolated-operator" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, `{}`)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"elevated": elevated.Load(), "assurance": "operator-token"})
	}))
	defer auth.Close()
	var torqueCalls atomic.Int64
	torque := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		torqueCalls.Add(1)
		http.Error(w, "Torque unavailable", http.StatusServiceUnavailable)
	}))
	defer torque.Close()
	db, err := localstore.Open(context.Background(), filepath.Join(t.TempDir(), "tachyon.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := New(Config{PluginBinDir: t.TempDir(), ApprovalQueueURL: auth.URL, Upstreams: catalog.Upstreams{TorqueURL: torque.URL}})
	s.store, s.objects = db, objects.New(db)
	host := httptest.NewServer(s.Routes())
	defer host.Close()
	client := &http.Client{Timeout: 5 * time.Second}
	call := func(method, path string, body any, cookie bool, want int) map[string]any {
		t.Helper()
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		req, err := http.NewRequest(method, host.URL+path, strings.NewReader(string(data)))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Origin", host.URL)
		req.Header.Set("Content-Type", "application/json")
		if cookie {
			req.AddCookie(&http.Cookie{Name: approvals.CookieName, Value: "isolated-operator"})
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		raw, err := io.ReadAll(resp.Body)
		if err != nil || resp.StatusCode != want {
			t.Fatalf("%s %s: status %d, want %d: %s (%v)", method, path, resp.StatusCode, want, raw, err)
		}
		if resp.Header.Get("Cache-Control") != "no-store" {
			t.Fatal("owned response may be cached")
		}
		var out map[string]any
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatal(err, string(raw))
		}
		return out
	}
	ideaID, secondID, inboxID, targetID := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	ideaPath := planner.Prefix + "/ideas/" + ideaID
	create := map[string]any{"id": ideaID, "fields": map[string]any{"title": "Before", "rank": 10}}
	call("POST", planner.Prefix+"/ideas", create, false, 401)
	call("POST", planner.Prefix+"/ideas", create, true, 201)
	call("POST", planner.Prefix+"/ideas", map[string]any{"id": secondID, "fields": map[string]any{"title": "Second", "rank": 20}}, true, 201)

	stream, err := client.Get(host.URL + planner.Prefix + "/events")
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Body.Close()
	if stream.StatusCode != 200 || stream.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatal("owned stream unavailable", stream.Status)
	}
	reader := bufio.NewReader(stream.Body)
	frame := func() string {
		t.Helper()
		var out strings.Builder
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				t.Fatal(err)
			}
			out.WriteString(line)
			if line == "\n" {
				return out.String()
			}
		}
	}
	initial := frame()
	// Even with authority, an old UI payload must not become an unguarded write.
	call("PATCH", planner.LegacyPrefix+"/ideas/"+ideaID, map[string]any{"rev": 1, "patch": map[string]any{"title": "Legacy"}}, true, 400)
	read := call("GET", ideaPath, nil, false, 200)
	if read["title"] != "Before" || read["rev"] != float64(1) {
		t.Fatal("rejected write changed the record", read)
	}
	updated := call("PATCH", ideaPath, map[string]any{"expected_revision": 1, "patch": map[string]any{"title": "After"}}, true, 200)
	conflict := call("PATCH", ideaPath, map[string]any{"expected_revision": 1, "patch": map[string]any{"title": "Stale"}}, true, 409)
	current := conflict["details"].(map[string]any)["current"].(map[string]any)
	if current["title"] != "After" || current["rev"] != updated["rev"] {
		t.Fatal("conflict does not expose the committed record", conflict)
	}
	commented := call("POST", ideaPath+"/comments", map[string]any{"expected_revision": updated["rev"], "text": "Isolated HTTP evidence"}, true, 200)
	read = call("GET", ideaPath, nil, false, 200)
	comments := read["comments"].([]any)
	if read["rev"] != commented["rev"] || comments[0].(map[string]any)["text"] != "Isolated HTTP evidence" {
		t.Fatal("comment readback differs", read)
	}
	changed := frame()
	if changed == initial || !strings.Contains(changed, "event: planner.changed") {
		t.Fatal("HTTP mutations did not invalidate the owned stream", changed)
	}
	// A stale member must roll back the entire reorder, including its first row.
	order := map[string]any{"items": []any{map[string]any{"id": ideaID, "expected_revision": commented["rev"]}, map[string]any{"id": secondID, "expected_revision": 99}}}
	call("POST", planner.Prefix+"/ideas/reorder", order, true, 409)
	read = call("GET", ideaPath, nil, false, 200)
	if read["rev"] != commented["rev"] || read["rank"] != float64(10) {
		t.Fatal("failed reorder partially committed", read)
	}
	inbox := call("POST", planner.Prefix+"/inbox", map[string]any{"id": inboxID, "fields": map[string]any{"title": "Capture"}}, true, 201)
	promotion := map[string]any{"expected_revision": inbox["rev"], "to_db": "ideas", "target_id": targetID}
	promotePath := planner.Prefix + "/inbox/" + inboxID + "/promote"
	promoted := call("POST", promotePath, promotion, true, 200)
	replayed := call("POST", promotePath, promotion, true, 200)
	if promoted["item"].(map[string]any)["id"] != targetID || replayed["item"].(map[string]any)["id"] != targetID {
		t.Fatal("promotion retry changed identity", promoted, replayed)
	}
	if got := call("GET", planner.Prefix+"/inbox/"+inboxID, nil, false, 200); got["status"] != "triaged" {
		t.Fatal("promotion did not triage source", got)
	}
	// Authority is rechecked on each write, not cached after the first success.
	elevated.Store(false)
	call("PATCH", ideaPath, map[string]any{"expected_revision": commented["rev"], "patch": map[string]any{"title": "Revoked"}}, true, 403)
	if got := call("GET", ideaPath, nil, false, 200); got["title"] != "After" || got["rev"] != commented["rev"] {
		t.Fatal("revoked authority changed the record", got)
	}
	if torqueCalls.Load() != 0 {
		t.Fatal("owned workflow called unavailable Torque", torqueCalls.Load())
	}
}

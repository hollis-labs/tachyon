package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hollis-labs/libs/mcp/plugin-mcp/plugin-sdk/subprocess"
	"github.com/hollis-labs/tachyon/pkg/pluginkit"
)

var rawTorque = map[string]string{
	"epics": `{"id":"EP-20261023-0002","name":"Approve and run","description":"Run approved actions.","status":"active","priority":2,"project_id":"PRJ-20260917-0001",
		"created_at":"2026-10-23T00:00:00Z","updated_at":"2026-10-23T00:00:00Z","system_prompt":"SECRET-PROMPT","secret":"SECRET-X","metadata":{"k":"SECRET-NESTED"}}`,
	"projects": `{"id":"PRJ-20260917-0001","name":"Tachyon","description":"The console.","repo_path":"/repo/tachyon","agent_path":"/SECRET-AGENT","read_paths":["/SECRET-R"],
		"write_paths":["/SECRET-W"],"permissions":{"a":"SECRET-P"},"rules":["SECRET-RULE"],"status":"active","icon":"x","created_at":"2026-10-23T00:00:00Z","updated_at":"2026-10-23T00:00:00Z"}`,
	"sprints": `{"id":"SP-20261023-0003","name":"Sprint 12","goal":"Ship previews.","status":"active","approval_mode":"manual","cost_budget":12.5,"project_id":"PRJ-20260917-0001",
		"started_at":"2026-10-23T00:00:00Z","ended_at":null,"created_at":"2026-10-23T00:00:00Z","updated_at":"2026-10-23T00:00:00Z"}`,
	"plans": `{"task":{"id":"PL-20261023-0004","title":"Roll out cards","status":"doing","description":"SECRET-DESC","system_prompt":"SECRET-PROMPT","working_dir":"/SECRET-DIR","updated_at":"2026-10-23T00:00:00Z"},
		"plan":{"version":1,"phases":[{"id":"p1","name":"Build","order":1,"acceptance":"tests pass","notes":"SECRET-NOTES"}]},
		"progress":{"total_children":4,"done":1,"blocked":0,"by_phase":{"p1":{"total":4,"done":1,"blocked":0,"tasks":["SECRET-T"]}}}}`,
}

func previewHandler(t *testing.T, status int, body func(r *http.Request) string) (*PreviewHandler, *[]*http.Request) {
	t.Helper()
	var seen []*http.Request
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body(r)))
	}))
	t.Cleanup(up.Close)
	return NewPreviewHandler(pluginkit.Route{Prefix: "/v1", Upstream: up.URL, Target: "/api/v1", Bearer: "hub-secret", Headers: map[string]string{"X-Client": "browser"}}), &seen
}

func TestTorquePreviewIsRebuiltFromAnAllowlistAndForwardsNothingFromTheBrowser(t *testing.T) {
	for kind, id := range map[string]string{"epics": "EP-20261023-0002", "projects": "PRJ-20260917-0001", "sprints": "SP-20261023-0003", "plans": "PL-20261023-0004"} {
		h, seen := previewHandler(t, 200, func(*http.Request) string { return rawTorque[kind] })
		res := h.Handle(context.Background(), subprocess.HTTPRequest{Method: "GET", Path: previewPrefix + "/" + kind + "/" + id, RawQuery: "include=everything",
			Headers: map[string]string{"Cookie": "tangent_participant=abc", "Authorization": "Bearer browser"}})
		if res.Status != 200 {
			t.Fatalf("%s: %d %s", kind, res.Status, res.Body)
		}
		if strings.Contains(string(res.Body), "SECRET") || strings.Contains(string(res.Body), "/repo/tachyon") && kind != "projects" {
			t.Errorf("%s leaked: %s", kind, res.Body)
		}
		var out map[string]any
		if json.Unmarshal(res.Body, &out) != nil || len(out) == 0 {
			t.Fatalf("%s: %s", kind, res.Body)
		}
		if len(*seen) != 1 {
			t.Fatalf("%s: %d upstream calls", kind, len(*seen))
		}
		up := (*seen)[0]
		wantPath := "/api/v1/" + kind + "/" + id
		if up.URL.Path != wantPath || up.URL.RawQuery != "" || up.Header.Get("Cookie") != "" || up.Header.Get("Authorization") != "Bearer hub-secret" || up.Method != "GET" {
			t.Errorf("%s: upstream saw %s %s?%s cookie=%q auth=%q", kind, up.Method, up.URL.Path, up.URL.RawQuery, up.Header.Get("Cookie"), up.Header.Get("Authorization"))
		}
		if res.Headers["Cache-Control"] != "no-store" {
			t.Errorf("%s cacheable", kind)
		}
	}
	// The shapes the overlay reads survive.
	h, _ := previewHandler(t, 200, func(*http.Request) string { return rawTorque["plans"] })
	res := h.Handle(context.Background(), subprocess.HTTPRequest{Method: "GET", Path: previewPrefix + "/plans/PL-20261023-0004"})
	var plan struct {
		Task struct{ Title, Status string }
		Plan struct {
			Phases []struct{ Name, Acceptance string }
		}
		Progress struct {
			Done    int
			ByPhase map[string]struct{ Total int } `json:"by_phase"`
		}
	}
	if err := json.Unmarshal(res.Body, &plan); err != nil || plan.Task.Title != "Roll out cards" || plan.Plan.Phases[0].Acceptance != "tests pass" || plan.Progress.ByPhase["p1"].Total != 4 || plan.Progress.Done != 1 {
		t.Errorf("plan shape lost: %v %s", err, res.Body)
	}
}

func TestTorquePreviewRefusesAnythingButAKnownKindAndIdBeforeUpstream(t *testing.T) {
	h, seen := previewHandler(t, 200, func(*http.Request) string { return "{}" })
	for _, c := range []struct{ method, path string }{
		{"POST", previewPrefix + "/epics/EP-20261023-0002"},
		{"PATCH", previewPrefix + "/epics/EP-20261023-0002"},
		{"GET", previewPrefix + "/tasks/CW-20261023-0001"},
		{"GET", previewPrefix + "/epics/EP-20261023-0002/comments"},
		{"GET", previewPrefix + "/epics/../tasks"},
		{"GET", previewPrefix + "/epics/EP-1"},
		{"GET", previewPrefix + "/epics/PRJ-20260917-0001"},
		{"GET", previewPrefix + "/epics"},
		{"GET", previewPrefix + "/epics/EP-20261023-0002%2f..%2ftasks"},
	} {
		if res := h.Handle(context.Background(), subprocess.HTTPRequest{Method: c.method, Path: c.path}); res.Status != 404 {
			t.Errorf("%s %s: %d", c.method, c.path, res.Status)
		}
	}
	if len(*seen) != 0 {
		t.Fatalf("%d requests reached Torque", len(*seen))
	}
}

func TestTorquePreviewFailsClosed(t *testing.T) {
	for _, c := range []struct {
		status int
		body   string
		want   int
	}{{404, `{"error":"SECRET"}`, 404}, {500, `SECRET stack`, 502}, {401, `SECRET`, 502}, {200, `not json SECRET`, 502}, {200, `[]`, 502}} {
		h, _ := previewHandler(t, c.status, func(*http.Request) string { return c.body })
		res := h.Handle(context.Background(), subprocess.HTTPRequest{Method: "GET", Path: previewPrefix + "/sprints/SP-20261023-0003"})
		if res.Status != c.want || strings.Contains(string(res.Body), "SECRET") {
			t.Errorf("upstream %d %q => %d %s", c.status, c.body, res.Status, res.Body)
		}
	}
	if res := NewPreviewHandler(pluginkit.Route{}).Handle(context.Background(), subprocess.HTTPRequest{Method: "GET", Path: previewPrefix + "/sprints/SP-20261023-0003"}); res.Status != 503 {
		t.Errorf("unconfigured: %d", res.Status)
	}
}

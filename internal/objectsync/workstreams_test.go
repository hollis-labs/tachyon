package objectsync

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hollis-labs/tachyon/internal/objects"
)

func TestWorkstreamsBoundedLifecycleAndLocalLinks(t *testing.T) {
	_, c := testStore(t)
	w := map[string]any{"id": "ws-1", "name": "Fabric", "status": "active", "workflow_id": "external-workflow", "created_at": "2026-10-01T00:00:00Z", "updated_at": "v1", "private": "secret"}
	payload := any(map[string]any{"workstreams": []any{w}})
	status := http.StatusOK
	server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/workstreams" || r.URL.Query().Get("limit") != "1000" || r.Header.Get("Tether-Protocol") != "1" {
			t.Error("wrong inventory request")
		}
		rw.WriteHeader(status)
		json.NewEncoder(rw).Encode(payload)
	}))
	defer server.Close()
	s, err := NewTetherWorkstreams(server.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	state := mustRun(t, c, s)
	first := get(t, c, s, "ws-1")
	var d objects.Workstream
	if err = json.Unmarshal(first.Definition, &d); err != nil {
		t.Fatal(err)
	}
	if state.Authoritative || d.WorkflowID != "" || d.Properties["tether"].(map[string]any)["workflow_ref"] != "external-workflow" || strings.Contains(string(first.Definition), "secret") {
		t.Fatal("unsafe normalization", first)
	}
	workflow, err := c.objects.Create(ctx, objects.Workflow{Common: objects.Common{Name: "Local workflow"}}, nil, "operator")
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.objects.Patch(ctx, first.ID, first.Revision, objects.Patch{Definition: json.RawMessage(fmt.Sprintf(`{"name":"My stream","workflow_id":%q}`, workflow.ID)), Metadata: json.RawMessage(`{"owner":"me"}`)}, "operator")
	if err != nil {
		t.Fatal(err)
	}
	w["status"], w["name"], w["workflow_id"], w["updated_at"] = "closed", "Renamed", "another-external", "v2"
	mustRun(t, c, s)
	closed := get(t, c, s, "ws-1")
	if err = json.Unmarshal(closed.Definition, &d); err != nil {
		t.Fatal(err)
	}
	if closed.ID != first.ID || closed.Deleted() || d.Status != "closed" || d.Name != "My stream" || d.WorkflowID != workflow.ID || string(closed.Metadata) != `{"owner":"me"}` {
		t.Fatal("close lost overrides or identity", closed)
	}
	rows, err := c.objects.ListPlanner(ctx, objects.PlannerFilter{Kind: objects.WorkstreamKind, Status: "closed"})
	if err != nil || len(rows) != 1 || rows[0].ID != first.ID {
		t.Fatal("workstream projection missing", err)
	}
	mustRun(t, c, s)
	if get(t, c, s, "ws-1").Revision != closed.Revision {
		t.Fatal("identical poll changed revision")
	}
	status = 503
	if _, err = c.Run(ctx, s, ""); err == nil {
		t.Fatal("failed request accepted")
	}
	status = 200
	for _, bad := range []any{map[string]any{}, map[string]any{"workstreams": nil}, map[string]any{"workstreams": []any{}, "cursor": "more"}, map[string]any{"workstreams": []any{w, w}}, map[string]any{"workstreams": []any{w, map[string]string{"id": "bad", "status": "deleted"}}}, map[string]any{"workstreams": []any{map[string]string{"name": "missing ID", "status": "active"}}}} {
		payload = bad
		if _, err = c.Run(ctx, s, ""); err == nil {
			t.Fatal("invalid scan accepted", bad)
		}
		if get(t, c, s, "ws-1").Revision != closed.Revision {
			t.Fatal("failed scan changed records")
		}
	}
	payload = map[string]any{"workstreams": []any{}}
	state = mustRun(t, c, s)
	absent := get(t, c, s, "ws-1")
	if state.Authoritative || absent.Deleted() || absent.Revision != closed.Revision {
		t.Fatal("bounded absence became deletion")
	}
	// A full bounded page also cannot establish completeness or delete prior rows.
	page := []any{}
	for i := 0; i < 100; i++ {
		page = append(page, map[string]string{"id": fmt.Sprintf("new-%d", i), "status": "active"})
	}
	payload = map[string]any{"workstreams": page}
	mustRun(t, c, s)
	if get(t, c, s, "ws-1").Deleted() {
		t.Fatal("full page tombstoned absent workstream")
	}
	w["status"] = "active"
	payload = map[string]any{"workstreams": []any{w}}
	mustRun(t, c, s)
	if get(t, c, s, "ws-1").ID != first.ID {
		t.Fatal("reopening changed identity")
	}
}

// Tether's list is paginated (the shape that failed every scan since 2026-10-19): the scan follows next_cursor
// to the end, accepts the pagination keys, still rejects unknown ones, and stays non-authoritative.
func TestWorkstreamsFollowTetherPagination(t *testing.T) {
	_, c := testStore(t)
	var cursors []string
	extra := ""
	server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		cur := r.URL.Query().Get("cursor")
		cursors = append(cursors, cur)
		body := map[string]any{"limit": 1000, "complete": false, "has_more": true, "next_cursor": "p2",
			"workstreams": []any{map[string]string{"id": "ws-a", "name": "A", "status": "active", "updated_at": "v1"}}}
		if cur == "p2" {
			body = map[string]any{"limit": 1000, "complete": true, "has_more": false, "next_cursor": "",
				"workstreams": []any{map[string]string{"id": "ws-b", "name": "B", "status": "closed", "updated_at": "v1"}}}
		}
		if extra != "" {
			body[extra] = true
		}
		json.NewEncoder(rw).Encode(body)
	}))
	defer server.Close()
	s, err := NewTetherWorkstreams(server.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	state := mustRun(t, c, s)
	if state.Authoritative || state.Pages != 2 || state.Items != 2 {
		t.Fatalf("state: %+v", state)
	}
	if len(cursors) != 2 || cursors[0] != "" || cursors[1] != "p2" {
		t.Fatalf("cursors followed: %q", cursors)
	}
	for _, id := range []string{"ws-a", "ws-b"} {
		if get(t, c, s, id).ID == "" {
			t.Fatalf("%s missing", id)
		}
	}
	extra = "surprise"
	if _, err = c.Run(ctx, s, ""); err == nil {
		t.Fatal("an unknown envelope key was accepted")
	}
}

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
		if r.Method != "GET" || r.URL.Path != "/workstreams" || r.URL.RawQuery != "" || r.Header.Get("Tether-Protocol") != "1" {
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

package objectsync

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/tachyon/internal/objects"
)

func sessionDTO(id, at string) map[string]any {
	return map[string]any{"id": id, "state": "running", "provider_id": "codex", "provider_kind": "cli", "created_at": at, "updated_at": at, "logical_agent_id": "worker", "project_id": "fabric", "workstream_id": "ws", "launch_id": "launch", "workspace": "private-path", "env": map[string]string{"TOKEN": "secret"}}
}

func TestSessionPaginationAtomicFailureAndNoAbsenceDeletion(t *testing.T) {
	_, c := testStore(t)
	a := sessionDTO("new", "2026-10-18T10:00:00Z")
	b := sessionDTO("old", "2026-10-17T10:00:00Z")
	first := any(map[string]any{"sessions": []any{a}, "next_cursor": a["created_at"]})
	second := any(map[string]any{"sessions": []any{b}})
	failSecond := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/sessions" || r.URL.Query().Get("limit") != "1000" || r.URL.Query().Get("state") != "" {
			t.Error("wrong inventory request")
		}
		if r.URL.Query().Get("cursor") == "" {
			json.NewEncoder(w).Encode(first)
		} else {
			if r.URL.Query().Get("cursor") != a["created_at"] {
				t.Error("wrong cursor")
			}
			if failSecond {
				w.WriteHeader(503)
				return
			}
			json.NewEncoder(w).Encode(second)
		}
	}))
	defer server.Close()
	s, err := NewTetherSessions(server.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	state := mustRun(t, c, s)
	r := get(t, c, s, "new")
	old := get(t, c, s, "old")
	if state.Authoritative || state.Pages != 2 || state.Items != 2 || r.Deleted() {
		t.Fatal("incorrect inventory", state)
	}
	if strings.Contains(string(r.Definition), "private-path") || strings.Contains(string(r.Definition), "secret") {
		t.Fatal("private fields imported")
	}
	var d objects.Session
	if err = json.Unmarshal(r.Definition, &d); err != nil || d.NativeSession != nil || d.ParentID != "" || d.StartedAt != nil {
		t.Fatal("invented identity/timestamps", err)
	}
	_, err = c.objects.Patch(ctx, r.ID, r.Revision, objects.Patch{Definition: json.RawMessage(`{"name":"My session"}`), Metadata: json.RawMessage(`{"keep":true}`)}, "operator")
	if err != nil {
		t.Fatal(err)
	}
	before := get(t, c, s, "new")
	a["state"] = "completed"
	a["ended_at"] = "2026-10-18T11:00:00Z"
	a["updated_at"] = "2026-10-18T11:00:00Z"
	failSecond = true
	if _, err = c.Run(ctx, s, ""); err == nil {
		t.Fatal("partial failure accepted")
	}
	if get(t, c, s, "new").Revision != before.Revision {
		t.Fatal("first page partially applied")
	}
	failSecond = false
	mustRun(t, c, s)
	completed := get(t, c, s, "new")
	if completed.ID != r.ID || completed.Name != "My session" || completed.Deleted() || string(completed.Metadata) != `{"keep":true}` {
		t.Fatal("completion lost local intent")
	}
	mustRun(t, c, s)
	if get(t, c, s, "new").Revision != completed.Revision {
		t.Fatal("identical scan created history")
	}
	for _, bad := range []any{map[string]any{}, map[string]any{"sessions": nil}, map[string]any{"sessions": []any{a, a}}, map[string]any{"sessions": []any{a}, "next_cursor": "bad"}, map[string]any{"sessions": []any{a}, "next_cursor": nil}, map[string]any{"sessions": []any{a}, "next_cursor": "2026-10-18T09:00:00Z"}, map[string]any{"sessions": []any{}, "more": true}} {
		first = bad
		if _, err = c.Run(ctx, s, ""); err == nil {
			t.Fatal("invalid page accepted", bad)
		}
		if get(t, c, s, "new").Revision != completed.Revision {
			t.Fatal("invalid scan changed history")
		}
	}
	first = map[string]any{"sessions": []any{a}, "next_cursor": a["created_at"]}
	second = first
	if _, err = c.Run(ctx, s, ""); err == nil {
		t.Fatal("repeating cursor accepted")
	}
	first = map[string]any{"sessions": []any{}}
	mustRun(t, c, s)
	if get(t, c, s, "old").Deleted() || get(t, c, s, "old").Revision != old.Revision {
		t.Fatal("missing session deleted")
	}
}

func TestSessionRelationshipResolutionUsesSourceFactsAndKeepsOverrides(t *testing.T) {
	_, c := testStore(t)
	dto := sessionDTO("s1", "2026-10-18T10:00:00Z")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"sessions": []any{dto}})
	}))
	defer server.Close()
	s, err := NewTetherSessions(server.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	mustRun(t, c, s)
	first := get(t, c, s, "s1")
	var d objects.Session
	json.Unmarshal(first.Definition, &d)
	if d.AgentID != "" || d.ProjectID != "" || d.WorkstreamID != "" {
		t.Fatal("unresolved refs became FKs")
	}
	seed := func(collection, id string, def objects.Definition) objects.Record {
		t.Helper()
		raw, _ := json.Marshal(def)
		r, err := c.objects.UpdateOrCreate(ctx, objects.Observation{Key: objects.SourceKey{SourceScope: objects.SourceScope{Provider: "tether", Instance: s.scope.Instance, Collection: collection, Kind: def.ObjectKind()}, ExternalID: id}, ObservedAt: time.Now().UTC(), Definition: raw}, "test")
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	agent := seed("catalog/agents", "worker", objects.Agent{Common: objects.Common{Name: "Display name"}})
	project := seed("registry/projects", "urn:fabric", objects.Project{Common: objects.Common{Name: "Unrelated name", Properties: map[string]any{"tether": map[string]any{"external_ids": []map[string]string{{"substrate": "tether", "external_id": "fabric"}}}}}})
	stream := seed("workstreams", "ws", objects.Workstream{Common: objects.Common{Name: "Work"}, Status: "active"})
	mustRun(t, c, s)
	resolved := get(t, c, s, "s1")
	json.Unmarshal(resolved.Definition, &d)
	if resolved.ID != first.ID || d.AgentID != agent.ID || d.ProjectID != project.ID || d.WorkstreamID != stream.ID {
		t.Fatal("exact refs not resolved", d)
	}
	local, err := c.objects.Create(ctx, objects.Project{Common: objects.Common{Name: "Mine"}}, nil, "operator")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]string{"project_id": local.ID})
	if _, err = c.objects.Patch(ctx, resolved.ID, resolved.Revision, objects.Patch{Definition: raw}, "operator"); err != nil {
		t.Fatal(err)
	}
	seed("registry/projects", "urn:ambiguous", objects.Project{Common: objects.Common{Name: "Other", Properties: map[string]any{"tether": map[string]any{"external_ids": []map[string]string{{"substrate": "tether", "external_id": "fabric"}}}}}})
	mustRun(t, c, s)
	observed := get(t, c, s, "s1")
	json.Unmarshal(observed.Definition, &d)
	if d.ProjectID != local.ID {
		t.Fatal("local mapping overwritten")
	}
	var source objects.Session
	json.Unmarshal(observed.Source.Definition, &source)
	if source.ProjectID != "" || source.Properties["tether"].(map[string]any)["project_resolution"] != "ambiguous" {
		t.Fatal("ambiguous alias resolved", source)
	}
}

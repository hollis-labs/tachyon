package objectsync

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/tachyon/internal/catalog"
	"github.com/hollis-labs/tachyon/internal/objects"
)

func TestAgentCatalogLifecycleAndAtomicRegistryFailure(t *testing.T) {
	_, c := testStore(t)
	a := map[string]any{"id": "worker", "name": "Worker", "system_prompt": "system.md", "agent_prompt": "agent.md", "roles": []string{"engineer"}, "skills": []string{"go"}, "mcp": map[string]any{"servers": []string{}}, "env": map[string]string{"TOKEN": "secret-env"}, "spec": map[string]string{"key": "secret-spec"}, "provider_overrides": map[string]any{"x": map[string]any{"env": map[string]string{"TOKEN": "secret-override"}}}}
	payload := any(map[string]any{"agents": []any{a}})
	registry := any(map[string]any{"agents": []any{map[string]string{"urn": "urn:worker", "kind": "agent", "status": "deprecated"}}})
	registryStatus := 200
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.Header.Get("Tether-Protocol") != "1" {
			t.Error("incorrect request")
		}
		switch r.URL.Path {
		case "/catalog/agents":
			json.NewEncoder(w).Encode(payload)
		case "/registry/agents":
			if r.URL.Query().Get("status") != "*" || r.URL.Query().Get("include") != "external_ids" {
				t.Error("filtered registry request")
			}
			w.WriteHeader(registryStatus)
			json.NewEncoder(w).Encode(registry)
		default:
			t.Errorf("unexpected route: %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	s, err := NewTetherAgents(server.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	state := mustRun(t, c, s)
	first := get(t, c, s, "worker")
	var d objects.Agent
	if err := json.Unmarshal(first.Definition, &d); err != nil {
		t.Fatal(err)
	}
	if !state.Authoritative || !d.Enabled || d.Runtime != "" || d.MachineID != "" || d.Instructions != "system.md" || len(d.Tools) != 0 || len(d.Roles) != 1 || len(d.Skills) != 1 {
		t.Fatalf("incorrect definition: %+v", d)
	}
	if strings.Contains(string(first.Definition), "secret-") || !strings.Contains(string(first.Definition), `"agent_prompt":"agent.md"`) || !strings.Contains(string(first.Definition), `"servers":[]`) {
		t.Fatal("wrong safe projection or MCP inheritance")
	}
	_, err = c.objects.Patch(ctx, first.ID, first.Revision, objects.Patch{Definition: json.RawMessage(`{"name":"My worker"}`), Metadata: json.RawMessage(`{"team":"fabric"}`)}, "operator")
	if err != nil {
		t.Fatal(err)
	}
	before := get(t, c, s, "worker")
	a["urn"], a["enabled"], a["name"] = "urn:worker", false, "Renamed"
	registryStatus = 503
	if _, err = c.Run(ctx, s, ""); err == nil {
		t.Fatal("accepted failed enrichment")
	}
	if get(t, c, s, "worker").Revision != before.Revision {
		t.Fatal("partial catalog was applied")
	}
	registryStatus = 200
	mustRun(t, c, s)
	disabled := get(t, c, s, "worker")
	if err := json.Unmarshal(disabled.Definition, &d); err != nil {
		t.Fatal(err)
	}
	if disabled.ID != first.ID || disabled.Deleted() || d.Enabled || disabled.Name != "My worker" || string(disabled.Metadata) != `{"team":"fabric"}` {
		t.Fatal("URN assignment/disable changed identity or local edits")
	}
	mustRun(t, c, s)
	if get(t, c, s, "worker").Revision != disabled.Revision {
		t.Fatal("identical poll created a revision")
	}
	for _, bad := range []any{map[string]any{}, map[string]any{"agents": nil}, map[string]any{"agents": []any{}, "cursor": "more"}, map[string]any{"agents": []any{a, a}}, map[string]any{"agents": []any{map[string]any{"name": "missing id"}}}} {
		payload = bad
		if _, err = c.Run(ctx, s, ""); err == nil {
			t.Fatal("accepted invalid catalog")
		}
		if get(t, c, s, "worker").Revision != disabled.Revision {
			t.Fatal("invalid scan changed data")
		}
	}
	payload = map[string]any{"agents": []any{a}}
	registry = map[string]any{"agents": []any{}, "cursor": "more"}
	if _, err = c.Run(ctx, s, ""); err == nil {
		t.Fatal("accepted incomplete registry")
	}
	registry = map[string]any{"agents": []any{}}
	payload = map[string]any{"agents": []any{}}
	mustRun(t, c, s)
	if !get(t, c, s, "worker").Deleted() {
		t.Fatal("missing definition not tombstoned")
	}
	a["enabled"] = true
	delete(a, "mcp")
	payload = map[string]any{"agents": []any{a}}
	mustRun(t, c, s)
	restored := get(t, c, s, "worker")
	if restored.Deleted() || restored.ID != first.ID || !strings.Contains(string(restored.Definition), `"servers":null`) {
		t.Fatal("restoration or inheritance lost")
	}
}

func TestAgentProjectLinksUseSourceIdentityAndResolveLater(t *testing.T) {
	_, c := testStore(t)
	projectRef := "catalog-project"
	projects := []any{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/catalog/agents":
			fmt.Fprint(w, `{"agents":[{"id":"worker","urn":"urn:worker"}]}`)
		case "/registry/agents":
			json.NewEncoder(w).Encode(map[string]any{"agents": []any{map[string]string{"urn": "urn:worker", "kind": "agent", "project": projectRef}}})
		case "/registry/projects":
			json.NewEncoder(w).Encode(map[string]any{"projects": projects})
		default:
			t.Error("unexpected endpoint")
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	agents, err := NewTetherAgents(server.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	ps, err := NewTetherProjects(server.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	assertLink := func(wantID, wantState string) objects.Record {
		t.Helper()
		r := get(t, c, agents, "worker")
		var d objects.Agent
		if err := json.Unmarshal(r.Source.Definition, &d); err != nil {
			t.Fatal(err)
		}
		if d.ProjectID != wantID || d.Properties["tether"].(map[string]any)["project_resolution"] != wantState {
			t.Fatalf("wrong project resolution: %s", r.Source.Definition)
		}
		return r
	}
	// A local project with a matching name is not an identifier correlation.
	local, err := c.objects.Create(ctx, objects.Project{Common: objects.Common{Name: projectRef}}, nil, "operator")
	if err != nil {
		t.Fatal(err)
	}
	mustRun(t, c, agents)
	first := assertLink("", "missing")
	p := project("Renamable display name", "active")
	p["external_ids"] = []map[string]string{{"substrate": "tether", "external_id": projectRef}}
	projects = []any{p}
	mustRun(t, c, ps)
	pr := get(t, c, ps, p["urn"].(string))
	// Effective aliases can be edited locally; resolution must use source data.
	_, err = c.objects.Patch(ctx, pr.ID, pr.Revision, objects.Patch{Definition: json.RawMessage(`{"properties":{"tether":{"external_ids":[{"substrate":"tether","external_id":"forged"}]}}}`)}, "operator")
	if err != nil {
		t.Fatal(err)
	}
	mustRun(t, c, agents)
	resolved := assertLink(pr.ID, "resolved")
	if resolved.ID != first.ID {
		t.Fatal("delayed target changed agent identity")
	}
	links, err := c.objects.Links(ctx, resolved.ID, false)
	if err != nil || len(links) != 1 || links[0].ToID != pr.ID {
		t.Fatalf("link missing: %+v %v", links, err)
	}
	projectRef = "forged"
	mustRun(t, c, agents)
	assertLink("", "missing")
	projectRef = p["urn"].(string)
	mustRun(t, c, agents)
	assertLink(pr.ID, "resolved")
	projectRef = "catalog-project"
	p2 := project("Another project", "active")
	p2["urn"], p2["external_ids"] = "urn:another", p["external_ids"]
	projects = []any{p, p2}
	mustRun(t, c, ps)
	mustRun(t, c, agents)
	ambiguous := assertLink("", "ambiguous")
	// Local relationship overrides remain authoritative even when source resolves.
	patch, _ := json.Marshal(map[string]string{"project_id": local.ID})
	_, err = c.objects.Patch(ctx, ambiguous.ID, ambiguous.Revision, objects.Patch{Definition: patch}, "operator")
	if err != nil {
		t.Fatal(err)
	}
	projects = []any{p}
	mustRun(t, c, ps)
	mustRun(t, c, agents)
	r := assertLink(pr.ID, "resolved")
	var effective objects.Agent
	if err := json.Unmarshal(r.Definition, &effective); err != nil {
		t.Fatal(err)
	}
	if effective.ProjectID != local.ID {
		t.Fatal("sync overwrote local relationship")
	}
	projects = []any{}
	mustRun(t, c, ps)
	mustRun(t, c, agents)
	assertLink("", "missing")
}

func TestCerberusUnixInventoryLifecycleAndSafeFacts(t *testing.T) {
	_, c := testStore(t)
	socket := filepath.Join(t.TempDir(), "cerberus.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	host := map[string]any{"name": "worker", "hostname": "worker.example", "tailnet_ip": "100.64.0.1", "tether_environment_id": "env:worker", "declared": map[string]any{"os": "linux", "arch": "amd64"}, "reach": map[string]any{"outpost": map[string]string{"token_file": "secret-path"}}}
	view := map[string]any{"host": host, "state": "offline", "stale": true, "probed_at": "2026-10-17T00:00:00Z", "checked_at": "2026-10-17T01:00:00Z", "probed": map[string]any{"os": "observed-linux", "cpu": 4, "tools": map[string]bool{"docker": true}, "secret": "secret-probe"}, "reason": "secret-reason"}
	payload := any([]any{view})
	status := 200
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/hosts" || r.URL.RawQuery != "" {
			t.Error("unexpected request or probe")
		}
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(payload)
	}))
	server.Listener.Close()
	server.Listener = listener
	server.Start()
	defer server.Close()
	s, err := NewCerberusMachines("unix:"+socket, "")
	if err != nil {
		t.Fatal(err)
	}
	mustRun(t, c, s)
	first := get(t, c, s, "worker")
	if first.Deleted() || s.Scope().Instance != "unix:"+socket || strings.Contains(string(first.Definition), "secret-") {
		t.Fatal("offline host deleted, bad scope, or credential leak")
	}
	var d objects.Machine
	if err := json.Unmarshal(first.Definition, &d); err != nil {
		t.Fatal(err)
	}
	if d.Platform != "linux" || d.Address != "100.64.0.1" || !strings.Contains(string(first.Definition), `"stale":true`) || !strings.Contains(string(first.Definition), "2026-10-17T00:00:00Z") || !strings.Contains(string(first.Definition), "env:worker") {
		t.Fatal("measurement provenance or correlation lost")
	}
	mustRun(t, c, s)
	if get(t, c, s, "worker").Revision != first.Revision {
		t.Fatal("polling refreshed stale measurements")
	}
	_, err = c.objects.Patch(ctx, first.ID, first.Revision, objects.Patch{Definition: json.RawMessage(`{"name":"My machine"}`), Metadata: json.RawMessage(`{"group":"fabric"}`)}, "operator")
	if err != nil {
		t.Fatal(err)
	}
	host["hostname"], host["tailnet_ip"], view["state"] = "renamed.example", "100.64.0.2", "unknown"
	mustRun(t, c, s)
	updated := get(t, c, s, "worker")
	if updated.ID != first.ID || updated.Name != "My machine" || updated.Deleted() {
		t.Fatal("address change lost identity or overlay")
	}
	for _, bad := range []any{nil, map[string]any{"hosts": []any{}}, []any{view, view}, []any{map[string]any{"host": host, "state": "unexpected"}}} {
		payload = bad
		if _, err = c.Run(ctx, s, ""); err == nil {
			t.Fatal("accepted unsafe host snapshot")
		}
		if get(t, c, s, "worker").Revision != updated.Revision {
			t.Fatal("failed scan mutated host")
		}
	}
	status, payload = 500, []any{}
	if _, err = c.Run(ctx, s, ""); err == nil || get(t, c, s, "worker").Deleted() {
		t.Fatal("failed empty snapshot inferred deletion")
	}
	status = 200
	mustRun(t, c, s)
	if !get(t, c, s, "worker").Deleted() {
		t.Fatal("missing declared host not tombstoned")
	}
	payload = []any{view}
	mustRun(t, c, s)
	restored := get(t, c, s, "worker")
	if restored.Deleted() || restored.ID != first.ID || string(restored.Metadata) != `{"group":"fabric"}` {
		t.Fatal("restoration lost history or local edits")
	}
}

func TestSourcesIncludeConfiguredCatalogs(t *testing.T) {
	sources, err := Sources(catalog.Upstreams{TetherAddr: "https://tether.example", CerberusAddr: "unix:/tmp/cerberus.sock"})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, s := range sources {
		seen[s.Name()] = true
	}
	if !seen["tether-projects"] || !seen["tether-agents"] || !seen["cerberus-machines"] {
		t.Fatal("configured source missing")
	}
}

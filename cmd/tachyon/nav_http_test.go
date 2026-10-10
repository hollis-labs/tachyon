package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hollis-labs/tachyon/internal/contract"
	"github.com/hollis-labs/tachyon/internal/plugins"
)

func TestHTTPNavEndpoint(t *testing.T) {
	root, mgr, logger := startupFixture(t)

	// First plugin: scm-ops declares valid module, verb, and nav group/item.
	scmDecl := `{
		"modules": ["scm"],
		"verbs": {"scm_status": {"effect": "reads"}},
		"nav": {
			"groups": [
				{"id": "code", "label": "Source Code", "icon": "git-branch", "priority": 10}
			],
			"items": [
				{"id": "scm-overview", "group": "code", "label": "Repositories", "route": "/scm", "priority": 10}
			]
		}
	}`
	fixturePlugin(t, root, "scm-ops", scmDecl, true)

	// Second plugin: work-ops declares valid module and verb, plus nav items including
	// a reserved route claim (/dashboard), a duplicate route claim (/scm), and a duplicate ID claim (scm-overview).
	workDecl := `{
		"modules": ["work"],
		"verbs": {"work_status": {"effect": "reads"}},
		"nav": {
			"groups": [
				{"id": "work", "label": "Work", "icon": "clipboard-list", "priority": 20}
			],
			"items": [
				{"id": "work-tasks", "group": "work", "label": "Tasks", "route": "/work/tasks", "priority": 20},
				{"id": "host-dashboard", "group": "work", "label": "Dashboard Claim", "route": "/dashboard"},
				{"id": "scm-dup-route", "group": "work", "label": "Duplicate Route", "route": "/scm"},
				{"id": "scm-overview", "group": "work", "label": "Duplicate ID", "route": "/work/scm-dup-id"}
			]
		}
	}`
	fixturePlugin(t, root, "work-ops", workDecl, true)

	// Admission must succeed: existing module/verb validation is satisfied,
	// while nav collisions and reserved route violations are warned and dropped.
	if err := loadStartupPlugins(context.Background(), mgr, root, logger); err != nil {
		t.Fatalf("startup admission failed: %v", err)
	}

	// Exercise production routing via newNavHandler on a real test server.
	mux := http.NewServeMux()
	mux.Handle("GET /api/nav", newNavHandler(mgr))
	server := httptest.NewServer(mux)
	defer server.Close()

	resp, err := http.Get(server.URL + "/api/nav")
	if err != nil {
		t.Fatalf("GET /api/nav request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected HTTP 200, got %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Fatalf("expected Content-Type application/json, got %q", ct)
	}

	var nav contract.NavDeclaration
	if err := json.NewDecoder(resp.Body).Decode(&nav); err != nil {
		t.Fatalf("failed to decode response JSON: %v", err)
	}

	// 1. Verify host-attributed nonempty contributions:
	if len(nav.Groups) != 2 {
		t.Fatalf("expected 2 groups, got %d: %+v", len(nav.Groups), nav.Groups)
	}
	groupMap := make(map[string]contract.NavGroup)
	for _, g := range nav.Groups {
		groupMap[g.ID] = g
	}
	if g, ok := groupMap["code"]; !ok || g.PluginID != "scm-ops" || g.Icon != "git-branch" {
		t.Fatalf("expected group code attributed to scm-ops with git-branch icon, got %+v", g)
	}
	if g, ok := groupMap["work"]; !ok || g.PluginID != "work-ops" || g.Icon != "clipboard-list" {
		t.Fatalf("expected group work attributed to work-ops with clipboard-list icon, got %+v", g)
	}

	if len(nav.Items) != 2 {
		t.Fatalf("expected 2 surviving items, got %d: %+v", len(nav.Items), nav.Items)
	}
	itemMap := make(map[string]contract.NavItem)
	for _, it := range nav.Items {
		itemMap[it.ID] = it
	}
	if it, ok := itemMap["scm-overview"]; !ok || it.PluginID != "scm-ops" || it.Route != "/scm" {
		t.Fatalf("expected scm-overview item attributed to scm-ops, got %+v", it)
	}
	if it, ok := itemMap["work-tasks"]; !ok || it.PluginID != "work-ops" || it.Route != "/work/tasks" {
		t.Fatalf("expected work-tasks item attributed to work-ops, got %+v", it)
	}

	// Ensure dropped items are absent from nav.Items:
	if _, exists := itemMap["host-dashboard"]; exists {
		t.Fatalf("reserved route item host-dashboard was not dropped")
	}
	if _, exists := itemMap["scm-dup-route"]; exists {
		t.Fatalf("duplicate route item scm-dup-route was not dropped")
	}

	// 2. Verify exposed collision and reserved diagnostics:
	if len(nav.Diagnostics) < 3 {
		t.Fatalf("expected at least 3 diagnostics, got %d: %+v", len(nav.Diagnostics), nav.Diagnostics)
	}

	var foundReserved, foundDupRoute, foundDupID bool
	for _, d := range nav.Diagnostics {
		switch {
		case d.Reason == contract.NavReservedRoute && d.Route == "/dashboard" && d.PluginID == "work-ops":
			foundReserved = true
		case d.Reason == contract.NavRouteCollision && d.Route == "/scm" && d.PluginID == "work-ops" && strings.Contains(d.Message, "scm-ops"):
			foundDupRoute = true
		case d.Reason == contract.NavIDCollision && d.ID == "scm-overview" && d.PluginID == "work-ops" && strings.Contains(d.Message, "scm-ops"):
			foundDupID = true
		}
	}
	if !foundReserved {
		t.Errorf("diagnostic for reserved route /dashboard not found in %+v", nav.Diagnostics)
	}
	if !foundDupRoute {
		t.Errorf("diagnostic for duplicate route /scm not found in %+v", nav.Diagnostics)
	}
	if !foundDupID {
		t.Errorf("diagnostic for duplicate ID scm-overview not found in %+v", nav.Diagnostics)
	}

	// 3. Verify human-readable notices:
	if len(nav.Notices) < 3 {
		t.Errorf("expected at least 3 notices, got %d: %+v", len(nav.Notices), nav.Notices)
	}
}

// Exercise both production handlers against actual admission and manager
// projection; source-supplied ownership and diagnostics never become authority.
func TestHTTPResolvedNavigationAndRegistry(t *testing.T) {
	root, mgr, logger := startupFixture(t)
	fixturePlugin(t, root, "z-owner", `{"modules":["owner"],"nav_schema":2,"nav":{"groups":[{"id":"work","label":"Owned Work","icon":"clipboard-list","priority":20,"owner":true,"plugin_id":"evil"}],"diagnostics":[{"reason":"spoof","plugin_id":"core"}]}}`, true)
	fixturePlugin(t, root, "a-client", `{"modules":["client"],"nav_schema":2,"nav":{"groups":[{"id":"work","label":"Losing Work"}],"pages":[{"id":"timeline","route":"/timeline","title":"Timeline","view":"timeline","plugin_id":"evil"},{"id":"hidden","route":"/hidden","title":"Hidden","view":"hidden","hidden":true}],"items":[{"id":"timeline","group_ref":"work","page":"timeline","label":"Timeline","plugin_id":"evil"},{"id":"orphan","group_ref":"absent","page":"hidden","hidden":true,"label":"Orphan"}]}}`, true)
	if err := loadStartupPlugins(context.Background(), mgr, root, logger); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle("GET /api/nav", newNavHandler(mgr))
	mux.Handle("GET /api/plugins/registry", newRegistryHandler(mgr))
	request := func(path string) []byte {
		t.Helper()
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != 200 || rec.Header().Get("Content-Type") != "application/json" {
			t.Fatalf("handler %s: %d %s", path, rec.Code, rec.Body.String())
		}
		return rec.Body.Bytes()
	}
	rawNav := request("/api/nav")
	var nav contract.NavDeclaration
	if err := json.Unmarshal(rawNav, &nav); err != nil {
		t.Fatal(err)
	}
	if len(nav.Items) != 0 || len(nav.Tree) != 2 || nav.Tree[0].OwnerID != "z-owner" || nav.Tree[0].Children[0].OwnerID != "a-client" || nav.Tree[1].ID != "more" || len(nav.Pages) != 2 {
		t.Fatalf("resolved HTTP nav %s", rawNav)
	}
	if nav.Projection == nil || nav.Projection.Status != contract.NavProjectionDegraded {
		t.Fatalf("no explicit projection status %s", rawNav)
	}
	rawRegistry := request("/api/plugins/registry")
	var response plugins.RegistryResponse
	if err := json.Unmarshal(rawRegistry, &response); err != nil {
		t.Fatal(err)
	}
	if err := response.Validate(); err != nil {
		t.Fatal(err)
	}
	if response.NavProjection.Status != nav.Projection.Status || len(response.Refusals) != 1 || len(response.Contributions["page"]) != 2 || len(response.Contributions["nav.item"]) != 2 {
		t.Fatalf("registry HTTP %s", rawRegistry)
	}
	for _, d := range response.Diagnostics {
		if d.Reason == "spoof" || d.PluginID == "core" || d.PluginID == "evil" {
			t.Fatal("plugin minted diagnostic")
		}
	}
	for _, byKey := range response.Contributions {
		for _, c := range byKey {
			if c.OwnerID == "core" || c.OwnerID == "evil" || c.Required || c.Representation != "declarative" {
				t.Fatalf("invalid projection authority %+v", c)
			}
		}
	}
}

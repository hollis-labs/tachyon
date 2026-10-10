package plugins

import (
	"bytes"
	"context"
	"log/slog"
	"testing"

	"github.com/hollis-labs/tachyon/internal/contract"
)

func TestResolveNavigation_Attribution(t *testing.T) {
	loadOrder := []string{"plugin-a", "plugin-b"}
	pluginNavs := map[string]*contract.NavDeclaration{
		"plugin-a": {
			Groups: []contract.NavGroup{
				{ID: "group-a", Label: "Group A", Icon: "users", Priority: 100},
			},
			Items: []contract.NavItem{
				{ID: "item-a", Label: "Item A", Group: "group-a", Route: "/a", Priority: 100},
			},
		},
		"plugin-b": {
			Groups: []contract.NavGroup{
				{ID: "group-b", Label: "Group B", Icon: "git-branch", Priority: 200},
			},
			Items: []contract.NavItem{
				// Even if a plugin supplies a malicious plugin_id, host sets real owner
				{ID: "item-b", Label: "Item B", Group: "group-b", Route: "/b", PluginID: "spoofed", Priority: 100},
			},
		},
	}

	res := ResolveNavigation(loadOrder, pluginNavs, nil)
	if len(res.Nav.Groups) != 2 || len(res.Nav.Items) != 2 {
		t.Fatalf("unexpected counts: groups=%d, items=%d", len(res.Nav.Groups), len(res.Nav.Items))
	}

	if res.Nav.Groups[0].PluginID != "plugin-a" || res.Nav.Groups[1].PluginID != "plugin-b" {
		t.Errorf("wrong group attribution: %+v", res.Nav.Groups)
	}
	if res.Nav.Items[0].PluginID != "plugin-a" || res.Nav.Items[1].PluginID != "plugin-b" {
		t.Errorf("wrong item attribution: %+v", res.Nav.Items)
	}
}

func TestResolveNavigation_ReservedRoutes(t *testing.T) {
	loadOrder := []string{"rogue-plugin", "config-ops"}
	pluginNavs := map[string]*contract.NavDeclaration{
		"rogue-plugin": {
			Groups: []contract.NavGroup{
				{ID: "rogue", Label: "Rogue", Icon: "terminal"},
			},
			Items: []contract.NavItem{
				{ID: "steal-dash", Label: "Dashboard", Group: "rogue", Route: "/dashboard"},
				{ID: "steal-recov", Label: "Recovery", Group: "rogue", Route: "/plugin-recovery"},
				{ID: "steal-sett", Label: "Settings", Group: "rogue", Route: "/settings"},
				{ID: "steal-sett-sub", Label: "Sub Settings", Group: "rogue", Route: "/settings/advanced"},
				{ID: "settings_like", Label: "Settings Like", Group: "rogue", Route: "/settings-like"},
				{ID: "legit", Label: "Legit", Group: "rogue", Route: "/rogue/legit"},
			},
		},
		"config-ops": {
			Groups: []contract.NavGroup{
				{ID: "settings", Label: "Settings", Icon: "settings"},
			},
			Items: []contract.NavItem{
				// config-ops is allowlisted to contribute /settings and its subpaths (DEC081)
				{ID: "config_list", Label: "Configuration", Group: "settings", Route: "/settings"},
				{ID: "config_child", Label: "Child Settings", Group: "settings", Route: "/settings/child"},
			},
		},
	}

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	res := ResolveNavigation(loadOrder, pluginNavs, logger)

	// rogue-plugin's 4 reserved claims dropped; config-ops's /settings and /settings/child,
	// plus rogue's /settings-like (boundary check) and /rogue/legit admitted (4 items total).
	if len(res.Nav.Items) != 4 {
		t.Fatalf("expected 4 admitted items, got %d: %+v", len(res.Nav.Items), res.Nav.Items)
	}

	routes := map[string]string{}
	for _, item := range res.Nav.Items {
		routes[item.Route] = item.PluginID
	}
	if routes["/settings"] != "config-ops" {
		t.Errorf("expected config-ops to own /settings, got %q", routes["/settings"])
	}
	if routes["/settings/child"] != "config-ops" {
		t.Errorf("expected config-ops to own /settings/child, got %q", routes["/settings/child"])
	}
	if routes["/settings-like"] != "rogue-plugin" {
		t.Errorf("expected rogue-plugin to own /settings-like (not reserved), got %q", routes["/settings-like"])
	}
	if routes["/rogue/legit"] != "rogue-plugin" {
		t.Errorf("expected rogue-plugin to own /rogue/legit, got %q", routes["/rogue/legit"])
	}

	// Verify diagnostics recorded 4 reserved_route rejections
	reservedDiags := 0
	for _, diag := range res.Nav.Diagnostics {
		if diag.Reason == "reserved_route" {
			reservedDiags++
		}
	}
	if reservedDiags != 4 {
		t.Errorf("expected 4 reserved_route diagnostics, got %d: %+v", reservedDiags, res.Nav.Diagnostics)
	}
}

func TestResolveNavigation_DuplicateRoutes(t *testing.T) {
	loadOrder := []string{"first-plugin", "second-plugin"}
	pluginNavs := map[string]*contract.NavDeclaration{
		"first-plugin": {
			Groups: []contract.NavGroup{{ID: "g1", Label: "G1", Icon: "activity"}},
			Items: []contract.NavItem{
				{ID: "first_item", Label: "First", Group: "g1", Route: "/duplicate-route"},
			},
		},
		"second-plugin": {
			Groups: []contract.NavGroup{{ID: "g2", Label: "G2", Icon: "activity"}},
			Items: []contract.NavItem{
				{ID: "second_item", Label: "Second", Group: "g2", Route: "/duplicate-route"},
			},
		},
	}

	res := ResolveNavigation(loadOrder, pluginNavs, nil)
	if len(res.Nav.Items) != 1 {
		t.Fatalf("expected 1 admitted item, got %d: %+v", len(res.Nav.Items), res.Nav.Items)
	}
	if res.Nav.Items[0].ID != "first_item" || res.Nav.Items[0].PluginID != "first-plugin" {
		t.Errorf("expected first_item to win, got: %+v", res.Nav.Items[0])
	}

	foundDiag := false
	for _, d := range res.Nav.Diagnostics {
		if d.Reason == "route_collision" && d.Route == "/duplicate-route" && d.PluginID == "second-plugin" {
			foundDiag = true
			break
		}
	}
	if !foundDiag {
		t.Errorf("missing route_collision diagnostic: %+v", res.Nav.Diagnostics)
	}
}

func TestResolveNavigation_DuplicateItemIDs(t *testing.T) {
	loadOrder := []string{"p1", "p2"}
	pluginNavs := map[string]*contract.NavDeclaration{
		"p1": {
			Groups: []contract.NavGroup{{ID: "g1", Label: "G1"}},
			Items:  []contract.NavItem{{ID: "same_item_id", Label: "P1 Item", Group: "g1", Route: "/p1"}},
		},
		"p2": {
			Groups: []contract.NavGroup{{ID: "g2", Label: "G2"}},
			Items:  []contract.NavItem{{ID: "same_item_id", Label: "P2 Item", Group: "g2", Route: "/p2"}},
		},
	}

	res := ResolveNavigation(loadOrder, pluginNavs, nil)
	if len(res.Nav.Items) != 1 {
		t.Fatalf("expected 1 admitted item, got %d: %+v", len(res.Nav.Items), res.Nav.Items)
	}
	if res.Nav.Items[0].PluginID != "p1" {
		t.Errorf("expected p1 to win duplicate item, got %s", res.Nav.Items[0].PluginID)
	}

	foundDiag := false
	for _, d := range res.Nav.Diagnostics {
		if d.Reason == "item_collision" && d.ItemID == "same_item_id" && d.PluginID == "p2" {
			foundDiag = true
			break
		}
	}
	if !foundDiag {
		t.Errorf("missing item_collision diagnostic: %+v", res.Nav.Diagnostics)
	}
}

func TestResolveNavigation_UnknownIcons(t *testing.T) {
	loadOrder := []string{"p1"}
	pluginNavs := map[string]*contract.NavDeclaration{
		"p1": {
			Groups: []contract.NavGroup{
				{ID: "valid-group", Label: "Valid", Icon: "clipboard-list"},
				{ID: "invalid-group", Label: "Invalid", Icon: "sparkles_magic"},
			},
			Items: []contract.NavItem{
				{ID: "item1", Group: "valid-group", Route: "/item1"},
			},
		},
	}

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	res := ResolveNavigation(loadOrder, pluginNavs, logger)

	// Both groups admitted (warn-and-drop policy, not startup refusal)
	if len(res.Nav.Groups) != 2 {
		t.Fatalf("expected 2 groups admitted, got %d", len(res.Nav.Groups))
	}

	foundDiag := false
	for _, d := range res.Nav.Diagnostics {
		if d.Reason == "unknown_icon" && d.GroupID == "invalid-group" {
			foundDiag = true
			break
		}
	}
	if !foundDiag {
		t.Errorf("missing unknown_icon diagnostic: %+v", res.Nav.Diagnostics)
	}
}

func TestManager_RestartReElectionAndDiagnostics(t *testing.T) {
	ctx := context.Background()
	var logs bytes.Buffer
	m := NewManager(slog.New(slog.NewTextHandler(&logs, nil)))

	declA := contract.PluginCapabilities{
		Modules: []string{"a"},
		Verbs:   map[string]contract.VerbDeclaration{"a_list": {Effect: contract.EffectReads}},
		Nav: &contract.NavDeclaration{
			Groups: []contract.NavGroup{{ID: "shared-group", Label: "Group A", Priority: 100}},
			Items:  []contract.NavItem{{ID: "item1", Group: "shared-group", Route: "/colliding-route"}},
		},
	}
	declB := contract.PluginCapabilities{
		Modules: []string{"b"},
		Verbs:   map[string]contract.VerbDeclaration{"b_list": {Effect: contract.EffectReads}},
		Nav: &contract.NavDeclaration{
			Groups: []contract.NavGroup{{ID: "shared-group", Label: "Group B", Priority: 200}},
			Items:  []contract.NavItem{{ID: "item2", Group: "shared-group", Route: "/colliding-route"}},
		},
	}

	procA := fakeProcess(t, "plugin-a", "declA", declA)
	procB := fakeProcess(t, "plugin-b", "declB", declB)

	if err := m.initializePlugin(ctx, procA); err != nil {
		t.Fatal(err)
	}
	if err := m.initializePlugin(ctx, procB); err != nil {
		t.Fatal(err)
	}

	nav := m.MergedNav()
	if len(nav.Items) != 1 || nav.Items[0].PluginID != "plugin-a" {
		t.Fatalf("expected plugin-a to own route initially, got: %+v", nav.Items)
	}

	// Detach plugin-a (simulates crash / restart removal)
	m.detachProcess(procA)

	// Now plugin-b should be the surviving owner of shared-group and /colliding-route
	navAfterDetach := m.MergedNav()
	if len(navAfterDetach.Items) != 1 || navAfterDetach.Items[0].PluginID != "plugin-b" {
		t.Fatalf("expected plugin-b to win after detach, got: %+v", navAfterDetach.Items)
	}
	if navAfterDetach.Groups[0].Label != "Group B" {
		t.Fatalf("expected Group B label after re-election, got: %s", navAfterDetach.Groups[0].Label)
	}
}

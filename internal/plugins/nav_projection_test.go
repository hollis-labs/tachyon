package plugins

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hollis-labs/libs/plugin-mcp/plugin-sdk/registry"
	"github.com/hollis-labs/tachyon/internal/contract"
)

func projectionManager(caps map[string]contract.PluginCapabilities) *Manager {
	m := NewManager(slog.New(slog.NewTextHandler(io.Discard, nil)))
	m.hostInstance = "projection-fixture"
	for id, c := range caps {
		copy := c
		m.plugins[id] = &pluginProcess{id: id, observeGeneration: "generation-" + id, capabilities: &copy}
		m.loadOrder = append(m.loadOrder, id)
	}
	return m
}
func projectCaps(caps map[string]contract.PluginCapabilities, order []string) NavResolutionResult {
	navs := map[string]*contract.NavDeclaration{}
	for id, c := range caps {
		nav, diags := contract.NormalizeNav(id, c)
		if nav != nil {
			nav.Diagnostics = diags
			navs[id] = nav
		}
	}
	return ResolveNavigation(order, navs, nil)
}
func hasProjectionDiag(nav contract.NavDeclaration, reason, kind, id string) bool {
	for _, d := range nav.Diagnostics {
		if d.Reason == reason && d.Kind == kind && d.ID == id {
			return true
		}
	}
	return false
}
func TestProjectionOwnerAndOrderIndependence(t *testing.T) {
	caps := map[string]contract.PluginCapabilities{}
	for _, id := range []string{"a-contributor", "b-contributor", "z-owner", "y-owner"} {
		owner := id == "z-owner" || id == "y-owner"
		caps[id] = contract.PluginCapabilities{NavSchema: 2, Nav: &contract.NavDeclaration{Groups: []contract.NavGroup{{ID: "shared", Label: id, Owner: owner, Icon: "users", Priority: 50}}, Items: []contract.NavItem{{ID: id, GroupRef: "shared", Label: id, Route: "/" + id, PluginID: "forged"}}}}
	}
	order := []string{"z-owner", "b-contributor", "a-contributor", "y-owner"}
	expected := projectCaps(caps, order)
	before, _ := json.Marshal(caps)
	for _, d := range expected.Nav.Diagnostics {
		if d.Reason == contract.NavGroupRedeclared && !strings.Contains(d.Message, `owner "y-owner" wins`) {
			t.Fatalf("notice named intermediate owner: %+v", d)
		}
	}

	if expected.Nav.Groups[0].PluginID != "y-owner" || expected.Nav.Groups[0].Label != "y-owner" {
		t.Fatalf("owner tier: %+v", expected.Nav.Groups)
	}
	if !hasProjectionDiag(expected.Nav, contract.NavGroupRedeclared, "nav.group", "shared") {
		t.Fatal("missing conflicting owner diagnostic")
	}
	for i := 0; i < 30; i++ {
		rand.New(rand.NewSource(int64(i))).Shuffle(len(order), func(a, b int) { order[a], order[b] = order[b], order[a] })
		got := projectCaps(caps, order)
		if !reflect.DeepEqual(got, expected) {
			t.Fatalf("load order changed output: %v", order)
		}
	}
	after, _ := json.Marshal(caps)
	if !bytes.Equal(before, after) {
		t.Fatal("projection mutated source")
	}
	for _, node := range expected.Nav.Tree[0].Children {
		if node.PluginID == "forged" || node.OwnerID != node.PluginID {
			t.Fatalf("spoofed node: %+v", node)
		}
	}
	resp := projectionManager(caps).BuildRegistry()
	if resp.NavProjection.Status != contract.NavProjectionDegraded || resp.Validate() != nil {
		t.Fatalf("registry %+v", resp.NavProjection)
	}
	if len(resp.Contributions["nav.item"]) != 4 {
		t.Fatal("metadata loser lost distinct items")
	}
}

func TestProjectionOrphanLifecycleAndRestartStableOwner(t *testing.T) {
	caps := map[string]contract.PluginCapabilities{
		"z-owner":  {NavSchema: 2, Nav: &contract.NavDeclaration{Groups: []contract.NavGroup{{ID: "work", Label: "Work", Owner: true}}}},
		"a-client": {NavSchema: 2, Nav: &contract.NavDeclaration{Items: []contract.NavItem{{ID: "timeline", GroupRef: "work", Route: "/timeline", Label: "Timeline"}}}},
	}
	m := projectionManager(caps)
	initial := m.BuildRegistry()
	if len(initial.Contributions["nav.group"]) != 1 {
		t.Fatal("missing owner")
	}
	owner := m.plugins["z-owner"]
	m.detachProcess(owner)
	// Retired/restarting processes carry no placement authority, even if recovery
	// retains their declaration metadata. Do not mint a registry core owner.
	absent := m.BuildRegistry()
	item := absent.Contributions["nav.item"]["a-client/timeline"]
	var metadata map[string]any
	_ = json.Unmarshal(item.Metadata, &metadata)
	if metadata["group"] != "more" || metadata["placement"] != "orphan" || item.OwnerID != "a-client" || absent.NavProjection.Status != contract.NavProjectionOK {
		t.Fatalf("orphan %+v %+v", metadata, absent.NavProjection)
	}
	nav := m.MergedNav()
	if len(nav.Tree) != 1 || nav.Tree[0].ID != "more" || nav.Tree[0].OwnerID != "core" || nav.Tree[0].Children[0].OwnerID != "a-client" {
		t.Fatalf("More tree %+v", nav.Tree)
	}
	if _, exists := absent.Plugins["core"]; exists || len(absent.Contributions["nav.group"]) != 0 {
		t.Fatal("minted core registry ownership")
	}
	m.mu.Lock()
	m.plugins["z-owner"] = owner
	m.loadOrder = append(m.loadOrder, "z-owner")
	m.registryRevision++
	m.mu.Unlock()
	restored := m.BuildRegistry()
	if !reflect.DeepEqual(restored.Contributions, initial.Contributions) {
		t.Fatal("restart changed projection")
	}
	// The watchdog dead marker is authoritative even before asynchronous detach.
	owner.dead.Store(true)
	dead := m.BuildRegistry()
	if _, ok := dead.Plugins["z-owner"]; ok {
		t.Fatal("dead plugin stayed active")
	}
	_ = json.Unmarshal(dead.Contributions["nav.item"]["a-client/timeline"].Metadata, &metadata)
	if metadata["group"] != "more" {
		t.Fatal("dead placement target admitted")
	}
}

func TestProjectionTopologyBoundsAndSparseIDs(t *testing.T) {
	caps := contract.PluginCapabilities{NavSchema: 2, Nav: &contract.NavDeclaration{
		Groups: []contract.NavGroup{{ID: "root", Label: "Root"}, {ID: "nested", Label: "Nested", Parent: "root"}, {ID: "deep", Label: "Deep", Parent: "nested"}, {ID: "too-deep", Parent: "deep"}, {ID: "cycle-one", Parent: "cycle-two"}, {ID: "cycle-two", Parent: "cycle-one"}, {ID: "lost", Parent: "absent"}},
		Items: []contract.NavItem{
			{ID: "sparse-999999", Group: "root", Route: "/root"}, {ID: "child", Parent: "sparse-999999", Route: "/child"}, {ID: "grandchild", Parent: "child", Route: "/grandchild"},
			{ID: "cyclic-a", Parent: "cyclic-b", Route: "/cycle-a"}, {ID: "cyclic-b", Parent: "cyclic-a", Route: "/cycle-b"}, {ID: "survivor", Parent: "cyclic-a", Route: "/survivor"},
			{ID: "nested-item", Group: "nested", Route: "/nested"}, {ID: "group-too-deep", Group: "deep", Route: "/deep"}, {ID: "foreign-local", Group: "absent", Route: "/foreign"},
		},
	}}
	res := projectCaps(map[string]contract.PluginCapabilities{"test": caps}, []string{"test"})
	for _, tc := range []struct{ reason, kind, id string }{
		{contract.NavDepthExceeded, "nav.group", "too-deep"}, {contract.NavParentCycle, "nav.group", "cycle-one"}, {contract.NavParentCycle, "nav.group", "cycle-two"},
		{contract.NavDepthExceeded, "nav.item", "grandchild"}, {contract.NavDepthExceeded, "nav.item", "group-too-deep"}, {contract.NavParentCycle, "nav.item", "cyclic-a"}, {contract.NavParentCycle, "nav.item", "cyclic-b"},
		{contract.NavOrphan, "nav.item", "survivor"}, {contract.NavOrphan, "nav.item", "foreign-local"},
	} {
		if !hasProjectionDiag(res.Nav, tc.reason, tc.kind, tc.id) {
			t.Errorf("missing %+v in %+v", tc, res.Nav.Diagnostics)
		}
	}
	if len(res.Nav.Pages) != 9 {
		t.Fatal("topology drop unregistered page")
	}
	var walk func([]*contract.NavNode, int)
	walk = func(nodes []*contract.NavNode, depth int) {
		for _, n := range nodes {
			if depth > 2 {
				t.Fatalf("tree too deep: %+v", n)
			}
			walk(n.Children, depth+1)
		}
	}
	walk(res.Nav.Tree, 0)
}

func TestProjectionHiddenPagesCollisionsAndHostAttribution(t *testing.T) {
	caps := map[string]contract.PluginCapabilities{
		"a": {NavSchema: 2, Nav: &contract.NavDeclaration{Groups: []contract.NavGroup{{ID: "g"}}, Pages: []contract.NavPage{{ID: "page", Route: "/shared", Title: "A", View: "a", PluginID: "core"}, {ID: "standalone", Route: "/hidden", Title: "Hidden", View: "a", Hidden: true}}, Items: []contract.NavItem{{ID: "hidden-item", Group: "g", Page: "standalone", Hidden: true}}, Subnav: []contract.NavSubnav{{ID: "orphan-sub", Parent: "missing", Page: "standalone", Placement: "top"}}}},
		"z": {NavSchema: 2, Nav: &contract.NavDeclaration{Pages: []contract.NavPage{{ID: "different", Route: "/shared", Title: "Z", View: "z"}}, Items: []contract.NavItem{{ID: "do-not-rebind", GroupRef: "g", Page: "different"}}}},
	}
	resp := projectionManager(caps).BuildRegistry()
	if err := resp.Validate(); err != nil {
		t.Fatal(err)
	}
	if resp.NavProjection.Status != contract.NavProjectionDegraded || len(resp.Contributions["page"]) != 2 || len(resp.Contributions["nav.item"]) != 1 || len(resp.Contributions["subnav.item"]) != 0 {
		t.Fatalf("counts/status %+v", resp)
	}
	p := resp.Contributions["page"]["a/page"]
	if p.OwnerID != "a" || p.OwnerGeneration != "generation-a" {
		t.Fatal("source minted ownership")
	}
	nav := projectionManager(caps).MergedNav()
	if len(nav.Items) != 0 || len(nav.Pages) != 2 || len(nav.Tree[0].Children) != 1 || !nav.Tree[0].Children[0].Hidden {
		t.Fatal("v2 leaked into legacy router or hidden page missing")
	}
}

func TestProjectionExplicitFailureAndEmptySuccess(t *testing.T) {
	m := projectionManager(nil)
	empty := m.BuildRegistry()
	if empty.NavProjection.Status != contract.NavProjectionOK || empty.Validate() != nil {
		t.Fatalf("empty not ok %+v", empty)
	}
	caps := map[string]contract.PluginCapabilities{"a": {Nav: &contract.NavDeclaration{Groups: []contract.NavGroup{{ID: "g"}}, Items: []contract.NavItem{{ID: "i", Group: "g", Route: "/a"}}}}}
	broken := projectionManager(caps)
	broken.hostInstance = "" // Force actual SDK structural rejection.
	failed := broken.BuildRegistry()
	if failed.NavProjection.Status != contract.NavProjectionFailed || failed.NavProjection.Reason == "" || len(failed.Contributions) != 0 || len(failed.Refusals) != 0 || len(failed.Plugins) != 1 {
		t.Fatalf("not explicit fail-safe %+v", failed)
	}
	if nav := broken.MergedNav(); nav.Projection.Status != contract.NavProjectionFailed || len(nav.Items) != 0 || len(nav.Tree) != 0 {
		t.Fatalf("HTTP view disagrees %+v", nav)
	}
	// A real structural collision (not an empty catalog) also invokes fail-safe.
	response := RegistryResponse{Response: registry.NewResponse("fixture", 1)}
	response.Plugins["a"] = registry.Plugin{OwnerGeneration: "gen"}
	resolved := projectCaps(caps, []string{"a"})
	resolved.Nav.Groups = append(resolved.Nav.Groups, resolved.Nav.Groups[0])
	projectRegistry(&response, resolved, nil)
	if response.NavProjection.Status != contract.NavProjectionFailed {
		t.Fatal("duplicate emission escaped validation")
	}
}

func TestProjectionDuplicateMalformedRefusalsRemainValid(t *testing.T) {
	var c contract.PluginCapabilities
	if err := json.Unmarshal([]byte(`{"nav":{"groups":[{"id":"g"}],"items":[{"id":"i","group":"g","route":"/i"},{"id":"i","route":"/again"},{"id":"i","route":"/third"},{"id":""},{"id":""}]}}`), &c); err != nil {
		t.Fatal(err)
	}
	resp := projectionManager(map[string]contract.PluginCapabilities{"a": c}).BuildRegistry()
	if resp.NavProjection.Status != contract.NavProjectionDegraded || resp.Validate() != nil || len(resp.Refusals) != 4 || len(resp.Contributions["nav.item"]) != 1 {
		t.Fatalf("entry-local diagnostics blanked document %+v", resp)
	}
}

func TestProjectionRefusalKeysCannotBeClaimedByPlugin(t *testing.T) {
	// Deliberately imitate the first duplicate's encoded host rejection key.
	key := "rejected-303a693a6e61762d6475706c69636174652d6964"
	c := contract.PluginCapabilities{Nav: &contract.NavDeclaration{Groups: []contract.NavGroup{{ID: "g"}}, Items: []contract.NavItem{{ID: "i", Group: "g", Route: "/i"}, {ID: "i", Group: "g", Route: "/again"}, {ID: key, Group: "g", Route: "/imitator"}}}}
	resp := projectionManager(map[string]contract.PluginCapabilities{"a": c}).BuildRegistry()
	if resp.NavProjection.Status != contract.NavProjectionDegraded || resp.Validate() != nil || len(resp.Contributions["nav.item"]) != 2 || len(resp.Refusals) != 1 {
		t.Fatalf("authored key blanked registry %+v", resp)
	}
	if resp.Refusals[0].LocalKey == key {
		t.Fatal("accepted and refused identities collide")
	}
}

func TestProjectionLongSparseChainIsBounded(t *testing.T) {
	nav := &contract.NavDeclaration{Groups: []contract.NavGroup{{ID: "root"}}}
	for i := 0; i < 200; i++ {
		id := fmt.Sprintf("n-%d", i*100003)
		parent := ""
		group := "root"
		if i > 0 {
			parent = fmt.Sprintf("n-%d", (i-1)*100003)
			group = ""
		}
		nav.Items = append(nav.Items, contract.NavItem{ID: id, Group: group, Parent: parent, Route: fmt.Sprintf("/n-%d", i), Label: id})
	}
	res := projectCaps(map[string]contract.PluginCapabilities{"a": {NavSchema: 2, Nav: nav}}, []string{"a"})
	if len(res.Nav.Items) != 2 || len(res.Nav.Pages) != 200 {
		t.Fatalf("unbounded placement survivors: %d", len(res.Nav.Items))
	}
	for _, d := range res.Nav.Diagnostics {
		if d.Dropped && d.Reason != contract.NavDepthExceeded {
			t.Fatalf("wrong chain reason %+v", d)
		}
	}
}

func TestProjectionManifestOrderSurvivesLocalDrops(t *testing.T) {
	c := contract.PluginCapabilities{Nav: &contract.NavDeclaration{Groups: []contract.NavGroup{{ID: "invalid/"}, {ID: "g", Label: "Good"}}, Items: []contract.NavItem{{ID: "bad/", Route: "/bad"}, {ID: "item", Group: "g", Route: "/item"}}}}
	resp := projectionManager(map[string]contract.PluginCapabilities{"a": c}).BuildRegistry()
	for _, kind := range []string{"nav.group", "nav.item", "page"} {
		for _, entry := range resp.Contributions[kind] {
			var meta map[string]any
			_ = json.Unmarshal(entry.Metadata, &meta)
			if meta["manifest_order"] != float64(1) {
				t.Fatalf("%s lost original index: %s", kind, entry.Metadata)
			}
		}
	}
}

func TestProjectionMenusAndSubnavRemainDeclarative(t *testing.T) {
	c := contract.PluginCapabilities{NavSchema: 2, Modules: []string{"work"}, Verbs: map[string]contract.VerbDeclaration{"work_list": {Effect: contract.EffectReads}}, Nav: &contract.NavDeclaration{Groups: []contract.NavGroup{{ID: "work"}}, Pages: []contract.NavPage{{ID: "p", Route: "/work", Title: "Tasks", View: "work"}}, Items: []contract.NavItem{{ID: "tasks", Group: "work", Page: "p"}}, Subnav: []contract.NavSubnav{{ID: "tab", Parent: "tasks", Page: "p", Placement: "top"}}, Menus: []contract.NavMenu{
		{ID: "refresh", Label: "Refresh", Region: "page", Target: &contract.NavMenuTarget{Page: "p"}, Action: json.RawMessage(`{"type":"command","command":"a/work_list","arguments":{}}`)},
		{ID: "navigate", Label: "Open", Region: "header", Action: json.RawMessage(`{"type":"navigate","route":"/work","parameters":{}}`)},
		{ID: "absent", Region: "header", Action: json.RawMessage(`{"type":"navigate","route":"/absent"}`)},
		{ID: "evil-command", Region: "header", Action: json.RawMessage(`{"type":"command","command":"foreign/work_list"}`)},
		{ID: "modal", Region: "header", Action: json.RawMessage(`{"type":"modal"}`)},
	}}}
	resp := projectionManager(map[string]contract.PluginCapabilities{"a": c}).BuildRegistry()
	if resp.Validate() != nil || resp.NavProjection.Status != contract.NavProjectionDegraded || len(resp.Contributions["subnav.item"]) != 1 || len(resp.Contributions["menu.item"]) != 2 || len(resp.Refusals) != 3 || len(resp.Contributions["command"]) != 0 {
		t.Fatalf("declarative menu projection %+v", resp)
	}
}

func TestProjectionDuringActualRestart(t *testing.T) {
	m := NewManager(slog.New(slog.NewTextHandler(io.Discard, nil)))
	ownerCaps := contract.PluginCapabilities{Modules: []string{"owner"}, NavSchema: 2, Nav: &contract.NavDeclaration{Groups: []contract.NavGroup{{ID: "work", Label: "Work", Owner: true}}}}
	owner := fakeProcess(t, "z-owner", "declared", ownerCaps)
	owner.binaryPath = "fixture-owner"
	if err := m.initializePlugin(context.Background(), owner); err != nil {
		t.Fatal(err)
	}
	clientCaps := contract.PluginCapabilities{Modules: []string{"client"}, NavSchema: 2, Nav: &contract.NavDeclaration{Items: []contract.NavItem{{ID: "timeline", GroupRef: "work", Route: "/timeline", Label: "Timeline"}}}}
	if err := m.initializePlugin(context.Background(), fakeProcess(t, "a-client", "declared", clientCaps)); err != nil {
		t.Fatal(err)
	}
	initial := m.BuildRegistry()
	started := make(chan struct{})
	release := make(chan struct{})
	result := make(chan error, 1)
	var closeOnce sync.Once
	unblock := func() { closeOnce.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	m.spawn = func(context.Context, string) (*pluginProcess, error) {
		close(started)
		<-release
		return fakeProcess(t, "z-owner", "declared", ownerCaps), nil
	}
	go func() { result <- m.RestartPlugin("z-owner") }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("restart did not detach before spawning")
	}
	pending := m.BuildRegistry()
	var metadata map[string]any
	_ = json.Unmarshal(pending.Contributions["nav.item"]["a-client/timeline"].Metadata, &metadata)
	if metadata["group"] != "more" || pending.NavProjection.Status != contract.NavProjectionOK {
		unblock()
		t.Fatalf("restart did not orphan client %+v", pending)
	}
	unblock()
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("restart did not finish")
	}
	restored := m.BuildRegistry()
	if len(restored.Contributions["nav.group"]) != 1 || restored.NavProjection.Status != contract.NavProjectionOK {
		t.Fatal("owner did not recover")
	}
	_ = json.Unmarshal(restored.Contributions["nav.item"]["a-client/timeline"].Metadata, &metadata)
	if metadata["group"] != "work" {
		t.Fatal("client placement not restored")
	}
	if restored.Revision <= initial.Revision {
		t.Fatal("restart did not advance revision")
	}
}

func TestProjectionWithActualRetiredTombstone(t *testing.T) {
	m, target := retiredFixture(t)
	c := contract.PluginCapabilities{Modules: []string{"client"}, NavSchema: 2, Nav: &contract.NavDeclaration{Items: []contract.NavItem{{ID: "timeline", GroupRef: "shared", Route: "/timeline", Label: "Timeline"}}}}
	if err := m.initializePlugin(context.Background(), fakeProcess(t, "a-client", "declared", c)); err != nil {
		t.Fatal(err)
	}
	resp := m.BuildRegistry()
	var metadata map[string]any
	_ = json.Unmarshal(resp.Contributions["nav.item"]["a-client/timeline"].Metadata, &metadata)
	if metadata["group"] != "more" || len(resp.RetiredPlugins) != 1 || resp.RetiredPlugins[0].ID != target.ID || resp.NavProjection.Status != contract.NavProjectionOK || resp.Validate() != nil {
		t.Fatalf("retired authority leaked %+v", resp)
	}
}

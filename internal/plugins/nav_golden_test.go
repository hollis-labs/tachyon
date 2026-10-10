package plugins

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/hollis-labs/libs/plugin-mcp/plugin-sdk/registry"
	"github.com/hollis-labs/tachyon/internal/contract"
)

func schema1ManifestCaps(t *testing.T) (map[string]contract.PluginCapabilities, []string) {
	t.Helper()
	caps := map[string]contract.PluginCapabilities{}
	ids := []string{}
	// Exact admitted old manifests, intentionally a migration fixture rather than
	// a manifest/icon/page-registry agreement check (DEC085 declined that check).
	for _, id := range []string{"agent-ops", "config-ops", "launch-ops", "observe-ops", "scm-ops", "service-ops", "session-ops", "work-ops"} {
		raw, err := os.ReadFile(filepath.Join("..", "..", "plugins", id, "capabilities.json"))
		if err != nil {
			t.Fatal(err)
		}
		var c contract.PluginCapabilities
		if err := json.Unmarshal(raw, &c); err != nil {
			t.Fatal(err)
		}
		if err := c.Validate(); err != nil {
			t.Fatal(err)
		}
		caps[id] = c
		ids = append(ids, id)
	}
	return caps, ids
}

func checkProjectionGolden(t *testing.T, name string, value any) {
	t.Helper()
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	raw = append(raw, '\n')
	path := filepath.Join("testdata", name)
	if os.Getenv("TACHYON_WRITE_PROJECTION_FIXTURE") == "1" {
		if err := os.MkdirAll("testdata", 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, raw, 0644); err != nil {
			t.Fatal(err)
		}
	}
	expected, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(raw, expected) {
		t.Fatalf("projection differs from reviewed golden %s", path)
	}
}

func TestSchema1OldNewProjectorGolden(t *testing.T) {
	caps, ids := schema1ManifestCaps(t)
	oldNavs := map[string]*contract.NavDeclaration{}
	for id, c := range caps {
		oldNavs[id] = c.Nav
	}
	old := legacyResolveNavigation(ids, oldNavs, nil).Nav
	checkProjectionGolden(t, "nav-schema1-old.json", old)
	actual := projectionManager(caps).BuildRegistry()
	if err := actual.Validate(); err != nil {
		t.Fatal(err)
	}
	plan, err := actual.Plan(registry.AdmissionPolicy{Kinds: actual.Kinds, Regions: actual.Regions})
	if err != nil || len(plan.Refusals) != 0 {
		t.Fatalf("actual registry admission %v %+v", err, plan.Refusals)
	}
	if actual.NavProjection.Status != contract.NavProjectionOK || len(actual.Contributions["page"]) != 15 {
		t.Fatal("legacy routes not registered")
	}
	// Extract visible legacy metadata from the actual registry contributions,
	// not from NormalizeNav. Synthesized pages and normalized all-of requirements
	// must preserve the legacy route, label, icon and priority attributions.
	view := contract.NavDeclaration{Groups: []contract.NavGroup{}, Items: []contract.NavItem{}, Diagnostics: []contract.NavDiagnostic{}, Notices: []string{}}
	for _, c := range actual.Contributions["nav.group"] {
		var g contract.NavGroup
		if err := json.Unmarshal(c.Metadata, &g); err != nil {
			t.Fatal(err)
		}
		g.PluginID = c.OwnerID
		view.Groups = append(view.Groups, g)
	}
	for _, c := range actual.Contributions["nav.item"] {
		var it contract.NavItem
		if err := json.Unmarshal(c.Metadata, &it); err != nil {
			t.Fatal(err)
		}
		p := actual.Contributions["page"][registry.QualifiedKey(c.OwnerID, it.Page)]
		var page contract.NavPage
		_ = json.Unmarshal(p.Metadata, &page)
		var payload struct {
			View string `json:"view"`
		}
		_ = json.Unmarshal(p.Declarative, &payload)
		if !page.Synthesized || page.Route != it.Route || payload.View != "legacy-route:"+it.Route {
			t.Fatalf("synthesis mismatch %+v %+v", it, page)
		}
		it.PluginID = c.OwnerID
		if len(it.RequiresVerbs) > 0 {
			it.RequiresVerb = it.RequiresVerbs[0]
		}
		it.RequiresVerbs = nil
		it.Page = ""
		view.Items = append(view.Items, it)
	}
	sort.Slice(view.Groups, func(i, j int) bool {
		a, b := view.Groups[i], view.Groups[j]
		if a.Priority != b.Priority {
			return a.Priority < b.Priority
		}
		return a.ID < b.ID
	})
	sort.Slice(view.Items, func(i, j int) bool {
		a, b := view.Items[i], view.Items[j]
		if a.Priority != b.Priority {
			return a.Priority < b.Priority
		}
		return a.ID < b.ID
	})
	if !reflect.DeepEqual(view, old) {
		oldRaw, _ := json.Marshal(old)
		newRaw, _ := json.Marshal(view)
		t.Fatalf("actual old/new projection mismatch\nold=%s\nnew=%s", oldRaw, newRaw)
	}
	checkProjectionGolden(t, "nav-schema1-registry.json", actual)
}

func TestHostNavDescriptorGolden(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "nav-schema1-registry.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture RegistryResponse
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	kinds, regions := NavDescriptors()
	if !reflect.DeepEqual(kinds, fixture.Kinds) || !reflect.DeepEqual(regions, fixture.Regions) {
		t.Fatal("published host descriptors differ from admitted fixture")
	}
	// Fresh descriptor calls cannot alter future host policy.
	kinds["page"] = registry.KindDescriptor{}
	d := regions["nav.rail"]
	d.Kinds[0] = "bad"
	regions["nav.rail"] = d
	againKinds, againRegions := NavDescriptors()
	if !reflect.DeepEqual(againKinds, fixture.Kinds) || !reflect.DeepEqual(againRegions, fixture.Regions) {
		t.Fatal("host policy aliased")
	}
}

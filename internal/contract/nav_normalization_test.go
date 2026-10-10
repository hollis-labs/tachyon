package contract

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
)

func normalizationCaps() PluginCapabilities {
	return PluginCapabilities{Modules: []string{"work"}, Verbs: map[string]VerbDeclaration{"work_list": {Effect: EffectReads}, "work_assign": {Effect: EffectWrites}}, NavSchema: 2,
		Nav: &NavDeclaration{Groups: []NavGroup{{ID: "work", Label: "Work"}}, Pages: []NavPage{{ID: "explicit", Route: "/work", Title: "Tasks", View: "work"}}, Items: []NavItem{{ID: "tasks", Label: "Tasks", Group: "work", Page: "explicit"}}}}
}
func hasNavReason(diags []NavDiagnostic, code string) bool {
	for _, d := range diags {
		if d.Reason == code {
			return true
		}
	}
	return false
}

func TestNormalizeNavOverlap(t *testing.T) {
	tests := []struct {
		name         string
		mutate       func(*PluginCapabilities)
		items, pages int
		page, reason string
	}{
		{"route and page agree", func(c *PluginCapabilities) { c.Nav.Items[0].Route = "/work" }, 1, 1, "explicit", ""},
		{"route and page disagree", func(c *PluginCapabilities) { c.Nav.Items[0].Route = "/other" }, 0, 1, "", NavRoutePageMismatch},
		{"route only synthesis", func(c *PluginCapabilities) { c.Nav.Items[0].Page = ""; c.Nav.Items[0].Route = "/other" }, 1, 2, "tasks", ""},
		{"explicit route wins synthesis", func(c *PluginCapabilities) { c.Nav.Items[0].Page = ""; c.Nav.Items[0].Route = "/work" }, 1, 1, "explicit", ""},
		{"page only", func(c *PluginCapabilities) {}, 1, 1, "explicit", ""},
		{"missing page", func(c *PluginCapabilities) { c.Nav.Items[0].Page = "absent" }, 0, 1, "", NavPageMissing},
		{"neither", func(c *PluginCapabilities) { c.Nav.Items[0].Page = "" }, 0, 1, "", NavPageMissing},
		{"undeclared singular", func(c *PluginCapabilities) { c.Nav.Items[0].RequiresVerb = "other_read" }, 0, 1, "", NavUndeclaredVerb},
		{"undeclared plural", func(c *PluginCapabilities) { c.Nav.Items[0].RequiresVerbs = []string{"work_list", "other_read"} }, 0, 1, "", NavUndeclaredVerb},
		{"empty declared requirement", func(c *PluginCapabilities) { c.Nav.Items[0].RequiresVerbs = []string{""} }, 0, 1, "", NavUndeclaredVerb},
		{"group and ref", func(c *PluginCapabilities) { c.Nav.Items[0].GroupRef = "other" }, 0, 1, "", NavPlacementConflict},
		{"group and parent", func(c *PluginCapabilities) { c.Nav.Items[0].Parent = "other" }, 0, 1, "", NavPlacementConflict},
		{"ref and parent", func(c *PluginCapabilities) {
			c.Nav.Items[0].Group = ""
			c.Nav.Items[0].GroupRef = "other"
			c.Nav.Items[0].Parent = "other"
		}, 0, 1, "", NavPlacementConflict},
		{"all placements", func(c *PluginCapabilities) { c.Nav.Items[0].GroupRef = "other"; c.Nav.Items[0].Parent = "other" }, 0, 1, "", NavPlacementConflict},
		{"missing placement orphan", func(c *PluginCapabilities) { c.Nav.Items[0].Group = "" }, 1, 1, "explicit", NavOrphan},
		{"missing local group orphan", func(c *PluginCapabilities) { c.Nav.Items[0].Group = "other" }, 1, 1, "explicit", NavOrphan},
		{"cross plugin ref deferred", func(c *PluginCapabilities) { c.Nav.Items[0].Group = ""; c.Nav.Items[0].GroupRef = "elsewhere" }, 1, 1, "explicit", ""},
		{"cross plugin parent deferred", func(c *PluginCapabilities) { c.Nav.Items[0].Group = ""; c.Nav.Items[0].Parent = "elsewhere" }, 1, 1, "explicit", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := normalizationCaps()
			tc.mutate(&c)
			before, _ := json.Marshal(c)
			nav, diags := NormalizeNav("work-ops", c)
			after, _ := json.Marshal(c)
			if string(before) != string(after) {
				t.Fatal("normalizer mutated source")
			}
			if len(nav.Items) != tc.items || len(nav.Pages) != tc.pages {
				t.Fatalf("nav=%+v diags=%+v", nav, diags)
			}
			if tc.items > 0 && (nav.Items[0].Page != tc.page || nav.Items[0].Route != nav.Pages[0].Route && tc.page == "explicit") {
				t.Fatalf("wrong page binding %+v", nav.Items)
			}
			if tc.reason != "" && !hasNavReason(diags, tc.reason) {
				t.Fatalf("missing %s: %+v", tc.reason, diags)
			}
			if tc.reason == "" && len(diags) > 0 {
				t.Fatalf("unexpected diagnostic %+v", diags)
			}
		})
	}
}

func TestNormalizeRequiredVerbsAllOfUnionAndNoAliases(t *testing.T) {
	c := normalizationCaps()
	c.Nav.Items[0].RequiresVerb = "work_list"
	c.Nav.Items[0].RequiresVerbs = []string{"work_assign", "work_list", "work_assign"}
	nav, diags := NormalizeNav("work-ops", c)
	if len(diags) > 0 || !reflect.DeepEqual(nav.Items[0].RequiresVerbs, []string{"work_list", "work_assign"}) {
		t.Fatalf("union %+v %+v", nav, diags)
	}
	nav.Items[0].RequiresVerbs[0] = "changed"
	nav.Groups[0].Label = "changed"
	nav.Pages[0].Route = "/changed"
	if c.Nav.Items[0].RequiresVerbs[0] != "work_assign" || c.Nav.Groups[0].Label != "Work" || c.Nav.Pages[0].Route != "/work" {
		t.Fatal("normalized output aliases source")
	}
}

func TestNormalizeSchemaProfiles(t *testing.T) {
	for _, schema := range []int{0, 1, 2, 3, -1} {
		t.Run(string(rune('a'+schema+1)), func(t *testing.T) {
			c := normalizationCaps()
			c.NavSchema = schema
			c.Nav.Items[0].Route = "/work"
			c.Nav.Items[0].Hidden = true
			c.Nav.Items[0].GroupRef = "other"
			c.Nav.Items[0].RequiresVerbs = []string{"not_declared"}
			c.Nav.Groups[0].Owner = true
			c.Nav.Groups[0].Footer = true
			n, d := NormalizeNav("work-ops", c)
			if schema < 0 {
				if len(n.Items) > 0 || !hasNavReason(d, NavMalformed) {
					t.Fatalf("bad schema %+v %+v", n, d)
				}
				return
			}
			if schema <= 1 {
				if len(n.Items) != 1 || n.Items[0].Hidden || n.Items[0].GroupRef != "" || len(n.Items[0].RequiresVerbs) > 0 || n.Groups[0].Owner || n.Groups[0].Footer || len(n.Pages) != 1 || !n.Pages[0].Synthesized || !hasNavReason(d, NavFieldIgnored) {
					t.Fatalf("legacy fields honored %+v %+v", n, d)
				}
			} else {
				if len(n.Items) > 0 || !hasNavReason(d, NavUndeclaredVerb) {
					t.Fatalf("v2 fields ignored %+v %+v", n, d)
				}
			}
			if (schema > 2) != hasNavReason(d, NavSchemaNewer) {
				t.Fatalf("newer diagnostic %+v", d)
			}
		})
	}
}

func TestNavRouteGrammarAndReservations(t *testing.T) {
	for _, route := range []string{"/work", "/work/board", "/a_1-b", "/work/_internal", "/work/-", "/work/:id", "/:owner/work/:work_id", "/" + strings.Repeat("a", 127)} {
		if !ValidNavRoute(route) {
			t.Errorf("valid route rejected %q", route)
		}
	}
	for _, route := range []string{"", "/", "/Work", "work", "/work/", "/a//b", "//a", "/_work", "/work?id=1", "/work#x", "/work/:id/:id", "/work/:Id", "/work/:1id", "/work/:id?", "/work/:id-extra", "/work/prefix:id", "/work/*", "/work/../x", "/" + strings.Repeat("a", 128)} {
		if ValidNavRoute(route) {
			t.Errorf("unsafe route accepted %q", route)
		}
	}
	for _, tc := range []struct {
		owner, route string
		allowed      bool
	}{
		{"config-ops", "/settings", true}, {"config-ops", "/settings/network", true}, {"evil", "/settings", false}, {"evil", "/settings/network", false}, {"config-ops", "/dashboard", false}, {"evil", "/plugin-recovery", false}, {"evil", "/dashboard/child", false}, {"evil", "/settings-other", true},
	} {
		c := normalizationCaps()
		c.Nav.Pages[0].Route = tc.route
		n, d := NormalizeNav(tc.owner, c)
		if (len(n.Pages) == 1) != tc.allowed || (!tc.allowed && !hasNavReason(d, NavReservedRoute)) {
			t.Errorf("%+v => %+v %+v", tc, n, d)
		}
	}
}

func TestNormalizeIDsOwnerAndSiblingBlastRadius(t *testing.T) {
	c := normalizationCaps()
	c.Nav.Groups = append(c.Nav.Groups, NavGroup{ID: "more"}, NavGroup{ID: "../bad"}, c.Nav.Groups[0])
	c.Nav.Items = append(c.Nav.Items, c.Nav.Items[0], NavItem{ID: ""}, NavItem{ID: "sparse_9001", Label: "Sparse", Group: "work", Route: "/sparse"})
	c.Nav.Pages = append(c.Nav.Pages, c.Nav.Pages[0], NavPage{ID: "invalid", Route: "/Bad", Title: "Bad", View: "bad"})
	c.Nav.Groups[0].PluginID = "core"
	c.Nav.Items[0].PluginID = "core"
	c.Nav.Pages[0].PluginID = "core"
	n, d := NormalizeNav("work-ops", c)
	if len(n.Groups) != 1 || len(n.Items) != 2 || len(n.Pages) != 2 {
		t.Fatalf("sibling blast radius %+v %+v", n, d)
	}
	for _, code := range []string{NavInvalidID, NavDuplicateID, NavReservedID, NavRouteInvalid} {
		if !hasNavReason(d, code) {
			t.Errorf("missing %s %+v", code, d)
		}
	}
	if n.Groups[0].PluginID != "work-ops" || n.Items[0].PluginID != "work-ops" || n.Pages[0].PluginID != "work-ops" {
		t.Fatal("trusted supplied owner")
	}
	n, d = NormalizeNav("core", c)
	if len(n.Groups) > 0 || len(n.Items) > 0 || len(n.Pages) > 0 || !hasNavReason(d, NavReservedID) {
		t.Fatalf("minted core owner %+v %+v", n, d)
	}
}

func TestNavV2WireFixtureRoundTrip(t *testing.T) {
	raw, err := os.ReadFile("testdata/nav-v2.json")
	if err != nil {
		t.Fatal(err)
	}
	var c PluginCapabilities
	if err := json.Unmarshal(raw, &c); err != nil {
		t.Fatal(err)
	}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	var a, b any
	_ = json.Unmarshal(raw, &a)
	_ = json.Unmarshal(encoded, &b)
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("descriptor fields lost: %s", encoded)
	}
	n, d := NormalizeNav("work-ops", c)
	if len(d) > 0 || len(n.Groups) != 1 || len(n.Items) != 1 || len(n.Pages) != 1 || len(n.Subnav) != 1 || len(n.Menus) != 1 || !n.Items[0].Hidden || !reflect.DeepEqual(n.Items[0].RequiresVerbs, []string{"work_list", "work_assign"}) {
		t.Fatalf("fixture %+v %+v", n, d)
	}
	for _, status := range []NavProjectionStatus{NavProjectionOK, NavProjectionDegraded, NavProjectionFailed} {
		raw, e := json.Marshal(NavProjection{Status: status, Revision: 7})
		if e != nil || !strings.Contains(string(raw), string(status)) {
			t.Fatalf("projection %s %v", raw, e)
		}
	}
}

func TestMalformedNavDoesNotPoisonNonNavOrSiblings(t *testing.T) {
	for _, fragment := range []string{
		`"nav":"invalid"`,
		`"nav":{"groups":"bad","items":[{"id":"tasks","route":"/work"}]}`,
		`"nav":{"items":[null,5,{"id":"bad","route":3},{"id":"good","label":"Good","route":"/good"}]}`,
		`"nav":{"pages":[{"id":"bad","route":false},{"id":"good","route":"/good","title":"Good","view":"good"}],"items":[{"id":"good","page":"good"}]}`,
		`"nav_schema":"future","nav":{"items":[{"id":"good","route":"/good"}]}`,
	} {
		raw := `{"modules":["work"],"verbs":{"work_list":{"effect":"reads"}},"nav_schema":2,` + fragment + `}`
		var c PluginCapabilities
		if err := json.Unmarshal([]byte(raw), &c); err != nil {
			t.Fatalf("nav poisoned decode: %v", err)
		}
		if err := c.Validate(); err != nil {
			t.Fatalf("nav poisoned validate: %v", err)
		}
		n, d := NormalizeNav("work-ops", c)
		if !hasNavReason(d, NavMalformed) {
			t.Fatalf("no structural diagnostic %+v", d)
		}
		if strings.Contains(fragment, `"id":"good"`) && !strings.Contains(fragment, `"nav_schema"`) && len(n.Items) != 1 {
			t.Fatalf("valid sibling lost %+v %+v", n, d)
		}
		encoded, err := json.Marshal(c)
		if err != nil {
			t.Fatal(err)
		}
		var carrier PluginCapabilities
		if err := json.Unmarshal(encoded, &carrier); err != nil {
			t.Fatal(err)
		}
		_, again := NormalizeNav("work-ops", carrier)
		if !hasNavReason(again, NavMalformed) {
			t.Fatalf("carrier erased structural failure %s", encoded)
		}
	}
	for _, raw := range []string{`{"modules":"bad","nav":{}}`, `{"modules":["work"],"verbs":[],"nav":{}}`, `{"modules":["work"],"settings":{"fields":"bad"},"nav":{}}`, `{`} {
		var c PluginCapabilities
		if json.Unmarshal([]byte(raw), &c) == nil {
			t.Fatalf("non-nav malformed type accepted %s", raw)
		}
	}
}

func TestSchemaOneIgnoresMalformedV2AndUnknownFutureFields(t *testing.T) {
	raw := `{"modules":["work"],"nav":{"groups":[{"id":"work","label":"Work","owner":3}],"items":[{"id":"good","route":"/good","group":"work","page":{},"hidden":false,"requires_verbs":"bad","future":"unknown"}],"pages":"bad","future":{"thing":1}},"future":true}`
	var c PluginCapabilities
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		t.Fatal(err)
	}
	n, d := NormalizeNav("work-ops", c)
	if len(n.Items) != 1 || len(n.Groups) != 1 || hasNavReason(d, NavMalformed) || !hasNavReason(d, NavFieldIgnored) {
		t.Fatalf("legacy v2 type affected old fields %+v %+v", n, d)
	}
	encoded, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), `"future"`) {
		t.Fatalf("unknown fields not dropped %s", encoded)
	}
	c.NavSchema = 9
	n, d = NormalizeNav("work-ops", c)
	if !hasNavReason(d, NavSchemaNewer) || !hasNavReason(d, NavMalformed) || len(n.Items) > 0 {
		t.Fatalf("future supported-fields policy %+v %+v", n, d)
	}
}

// Compatibility scaffolding for the eight unchanged legacy manifests. This is
// not the old-MergedNav/new-registry projection golden (0117) or actual Go/TS
// catalog descriptor parity (0117/0118), whose checks are approved by DEC085.
func TestEightLegacyManifestNormalizationCompatibility(t *testing.T) {
	raw, err := os.ReadFile("testdata/nav-schema1-normalization.json")
	if err != nil {
		t.Fatal(err)
	}
	var expected map[string]json.RawMessage
	if err := json.Unmarshal(raw, &expected); err != nil {
		t.Fatal(err)
	}
	for owner, want := range expected {
		t.Run(owner, func(t *testing.T) {
			manifest, err := os.ReadFile("../../plugins/" + owner + "/capabilities.json")
			if err != nil {
				t.Fatal(err)
			}
			var c PluginCapabilities
			if err := json.Unmarshal(manifest, &c); err != nil {
				t.Fatal(err)
			}
			if err := c.Validate(); err != nil {
				t.Fatal(err)
			}
			n, d := NormalizeNav(owner, c)
			if len(d) > 0 {
				t.Fatalf("legacy diagnostic %+v", d)
			}
			got, err := json.Marshal(n)
			if err != nil {
				t.Fatal(err)
			}
			var a, b any
			_ = json.Unmarshal(want, &a)
			_ = json.Unmarshal(got, &b)
			if !reflect.DeepEqual(a, b) {
				t.Fatalf("legacy normalization changed\nwant %s\ngot %s", want, got)
			}
		})
	}
}

func TestHiddenPageAndIndependentSiblingSurviveItemDrop(t *testing.T) {
	c := normalizationCaps()
	c.Nav.Items[0].Hidden = true
	c.Nav.Items[0].RequiresVerb = "not_declared"
	c.Nav.Pages[0].Hidden = true
	c.Nav.Subnav = []NavSubnav{{ID: "good_tab", Label: "Tab", Parent: "external_parent", Page: "explicit", Placement: "left", RequiresVerbs: []string{"work_list", "work_list"}}, {ID: "bad_tab", Parent: "tasks", Page: "missing", Placement: "top"}}
	c.Nav.Menus = []NavMenu{{ID: "good_menu", Label: "Open", Region: "header", Action: json.RawMessage(`{"type":"navigate","route":"/work"}`)}, {ID: "bad_menu", RequiresVerbs: []string{"undeclared"}}}
	n, d := NormalizeNav("work-ops", c)
	if len(n.Items) != 0 || len(n.Pages) != 1 || !n.Pages[0].Hidden || len(n.Subnav) != 1 || len(n.Menus) != 1 || !hasNavReason(d, NavPageMissing) || !hasNavReason(d, NavUndeclaredVerb) {
		t.Fatalf("unrelated routable page or entries lost %+v %+v", n, d)
	}
	n.Subnav[0].RequiresVerbs[0] = "changed"
	n.Menus[0].Action[0] = '['
	if c.Nav.Subnav[0].RequiresVerbs[0] != "work_list" || c.Nav.Menus[0].Action[0] != '{' {
		t.Fatal("child declaration aliases source")
	}
}

func TestUntrustedOutputFieldsAreNotHostAuthority(t *testing.T) {
	raw := `{"modules":["work"],"nav_schema":2,"nav":{"diagnostics":[{"reason":"invented","plugin_id":"core","dropped":true}],"notices":["invented"],"groups":[{"id":"work","label":"Work","plugin_id":"core","owner":true}],"pages":[{"id":"page","route":"/work","title":"Work","view":"work","synthesized":true,"plugin_id":"core"}],"items":[{"id":"item","label":"Work","group":"work","page":"page","plugin_id":"other"}]}}`
	var c PluginCapabilities
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		t.Fatal(err)
	}
	n, d := NormalizeNav("work-ops", c)
	if len(d) != 0 || len(n.Diagnostics) != 0 || len(n.Notices) != 0 || n.Pages[0].Synthesized || n.Groups[0].PluginID != "work-ops" || n.Items[0].PluginID != "work-ops" || n.Pages[0].PluginID != "work-ops" {
		t.Fatalf("source asserted host authority %+v %+v", n, d)
	}
}

func TestIgnoredZeroValueFieldsSurvivePluginCarrier(t *testing.T) {
	var c PluginCapabilities
	raw := `{"modules":["work"],"nav":{"items":[{"id":"item","label":"Item","route":"/work","hidden":false,"footer":false,"requires_verbs":[]}]}}`
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	var again PluginCapabilities
	if err := json.Unmarshal(encoded, &again); err != nil {
		t.Fatal(err)
	}
	n, d := NormalizeNav("work-ops", again)
	fields := map[string]bool{}
	for _, diag := range d {
		if diag.Reason == NavFieldIgnored {
			fields[diag.Message] = true
			if !diag.Informational || diag.Dropped {
				t.Fatal("ignored fields dropped an entry")
			}
		}
	}
	for _, field := range []string{"hidden", "footer", "requires_verbs"} {
		if !fields["schema 1 ignores "+field] {
			t.Fatalf("carrier erased field %s: %s %+v", field, encoded, d)
		}
	}
	if len(n.Items) != 1 {
		t.Fatal("ignored fields removed entry")
	}
}

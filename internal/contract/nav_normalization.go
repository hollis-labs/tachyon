package contract

import (
	"fmt"
	"regexp"
	"strings"
)

const (
	NavInvalidID         = "nav-invalid-id"
	NavDuplicateID       = "nav-duplicate-id"
	NavIDCollision       = "nav-id-collision"
	NavUndeclaredVerb    = "nav-undeclared-verb"
	NavRouteInvalid      = "nav-route-invalid"
	NavReservedRoute     = "nav-reserved-route"
	NavRouteCollision    = "nav-route-collision"
	NavRoutePageMismatch = "nav-route-page-mismatch"
	NavPageMissing       = "nav-page-missing"
	NavPlacementConflict = "nav-placement-conflict"
	NavOrphan            = "nav-orphan"
	NavParentCycle       = "nav-parent-cycle"
	NavDepthExceeded     = "nav-depth-exceeded"
	NavReservedID        = "nav-reserved-id"
	NavGroupRedeclared   = "nav-group-redeclared"
	NavSubnavOrphan      = "nav-subnav-orphan"
	NavMenuInvalidAction = "nav-menu-invalid-action"
	NavFieldIgnored      = "nav-field-ignored"
	NavSchemaNewer       = "nav-schema-newer"
	NavIconUnknown       = "nav-icon-unknown"
	// Structural type errors have the same entry-local policy as other defects.
	NavMalformed = "nav-malformed"
)

var navRouteFirstLiteral = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)
var navRouteLiteral = regexp.MustCompile(`^[a-z0-9_-]+$`)
var navRouteParameter = regexp.MustCompile(`^:[a-z][a-z0-9_]*$`)

func ValidNavRoute(route string) bool {
	if len(route) > 128 || !strings.HasPrefix(route, "/") {
		return false
	}
	names := make(map[string]bool)
	for index, segment := range strings.Split(route[1:], "/") {
		if navRouteParameter.MatchString(segment) {
			if names[segment] {
				return false
			}
			names[segment] = true
			continue
		}
		literal := navRouteLiteral
		if index == 0 {
			literal = navRouteFirstLiteral
		}
		if !literal.MatchString(segment) {
			return false
		}
	}
	return true
}

// ReservedNavRoute is the shared DEC081 allowlist. Host-only routes include
// their descendants, matching the existing resolution helper.
func ReservedNavRoute(route string) (owner string, reserved bool) {
	for _, prefix := range []string{"/dashboard", "/plugin-recovery", "/settings"} {
		if route == prefix || strings.HasPrefix(route, prefix+"/") {
			if prefix == "/settings" {
				return "config-ops", true
			}
			return "", true
		}
	}
	return "", false
}

// NormalizeNav is pure: it preserves the authored capabilities and independently
// returns locally admitted declarations and host-attributed diagnostics. It does
// not resolve cross-plugin ownership, placement topology, menu actions, or view
// availability: those require the projector/catalog/action gateway. In particular
// missing placement remains authored (no synthesized core owner or More group).
func NormalizeNav(pluginID string, pc PluginCapabilities) (*NavDeclaration, []NavDiagnostic) {
	if pc.Nav == nil {
		return nil, nil
	}
	out := &NavDeclaration{}
	var diags []NavDiagnostic
	add := func(code, kind, id, route, msg string, info, drop bool) {
		d := NavDiagnostic{Reason: code, PluginID: pluginID, Kind: kind, ID: id, Route: route, Message: msg, Informational: info, Dropped: drop}
		if kind == "nav.group" {
			d.GroupID = id
		} else if kind == "nav.item" {
			d.ItemID = id
		}
		diags = append(diags, d)
	}
	schema := pc.NavSchema
	if schema == 0 {
		schema = 1
	}
	if schema > 2 {
		add(NavSchemaNewer, "nav", "", "", "newer nav schema; only supported schema-2 fields honored", true, false)
	}
	if schema < 1 || len(pc.navSchemaMalformed) > 0 {
		add(NavMalformed, "nav", "", "", "invalid nav_schema; navigation dropped", false, true)
		return out, diags
	}
	if len(pc.Nav.malformed) > 0 {
		add(NavMalformed, "nav", "", "", "navigation must be an object", false, true)
		return out, diags
	}
	malformedEntries := map[string]bool{}
	for _, issue := range pc.Nav.decodeIssues {
		if schema == 1 && isV2NavField(issue.kind, issue.field) {
			continue
		}
		key := fmt.Sprintf("%s/%d", issue.kind, issue.index)
		if malformedEntries[key] {
			continue
		}
		malformedEntries[key] = true
		add(NavMalformed, issue.kind, issue.id, "", fmt.Sprintf("invalid navigation field %s", issue.field), false, true)
	}
	ignored := func(kind, id, field string) {
		add(NavFieldIgnored, kind, id, "", "schema 1 ignores "+field, true, false)
	}
	for _, presence := range pc.Nav.presence {
		if schema == 1 && isV2NavField(presence.kind, presence.field) {
			ignored(presence.kind, presence.id, presence.field)
		}
	}
	invalid := func(kind string, index int) bool {
		for _, issue := range pc.Nav.decodeIssues {
			if issue.kind == kind && (issue.index == index || issue.index < 0) && !(schema == 1 && isV2NavField(kind, issue.field)) {
				return true
			}
		}
		return false
	}
	seen := map[string]map[string]bool{}
	idOK := func(kind, id string) bool {
		if !declarationIDPattern.MatchString(id) {
			add(NavInvalidID, kind, id, "", "invalid navigation id", false, true)
			return false
		}
		if pluginID == "core" || (kind == "nav.group" && id == "more") {
			add(NavReservedID, kind, id, "", "reserved navigation identity", false, true)
			return false
		}
		if seen[kind] == nil {
			seen[kind] = map[string]bool{}
		}
		if seen[kind][id] {
			add(NavDuplicateID, kind, id, "", "duplicate navigation id; first entry wins", false, true)
			return false
		}
		seen[kind][id] = true
		return true
	}
	verbsOK := func(kind, id string, verbs []string) bool {
		for _, verb := range verbs {
			if _, ok := pc.Verbs[verb]; !ok {
				add(NavUndeclaredVerb, kind, id, "", "required verb not declared by owner: "+verb, false, true)
				return false
			}
		}
		return true
	}
	routeOK := func(id, route string) bool {
		if !ValidNavRoute(route) {
			add(NavRouteInvalid, "page", id, route, "invalid route identity", false, true)
			return false
		}
		if owner, reserved := ReservedNavRoute(route); reserved && (owner == "" || owner != pluginID) {
			add(NavReservedRoute, "page", id, route, "reserved host route", false, true)
			return false
		}
		return true
	}
	for i, g := range pc.Nav.Groups {
		if invalid("nav.group", i) || !idOK("nav.group", g.ID) {
			continue
		}
		if schema == 1 {
			if len(pc.Nav.presence) == 0 && (g.Parent != "" || g.Kind != "" || g.Collapsed || g.Footer || g.Owner) {
				ignored("nav.group", g.ID, "v2 group hints")
			}
			g.Parent = ""
			g.Kind = ""
			g.Collapsed = false
			g.Footer = false
			g.Owner = false
		}
		g.ManifestOrder = i
		g.PluginID = pluginID
		out.Groups = append(out.Groups, g)
	}
	pages := map[string]NavPage{}
	routes := map[string]string{}
	if schema >= 2 {
		for i, p := range pc.Nav.Pages {
			if invalid("page", i) || !idOK("page", p.ID) {
				continue
			}
			p.RequiresVerbs = unionVerbs("", p.RequiresVerbs)
			if !verbsOK("page", p.ID, p.RequiresVerbs) || !routeOK(p.ID, p.Route) {
				continue
			}
			if p.View == "" || p.Title == "" {
				add(NavMalformed, "page", p.ID, p.Route, "page requires title and host view", false, true)
				continue
			}
			if _, ok := routes[p.Route]; ok {
				add(NavRouteCollision, "page", p.ID, p.Route, "explicit page route already admitted", false, true)
				continue
			}
			p.ManifestOrder = i
			p.PluginID = pluginID
			p.Synthesized = false
			pages[p.ID] = p
			routes[p.Route] = p.ID
			out.Pages = append(out.Pages, p)
		}
	} else if len(pc.Nav.presence) == 0 {
		if len(pc.Nav.Pages) > 0 {
			ignored("nav", "", "pages")
		}
		if len(pc.Nav.Subnav) > 0 {
			ignored("nav", "", "subnav")
		}
		if len(pc.Nav.Menus) > 0 {
			ignored("nav", "", "menus")
		}
	}
	for i, item := range pc.Nav.Items {
		if invalid("nav.item", i) || !idOK("nav.item", item.ID) {
			continue
		}
		if schema == 1 {
			if len(pc.Nav.presence) == 0 && (item.GroupRef != "" || item.Parent != "" || item.Page != "" || item.Hidden || item.Icon != "" || item.Footer || len(item.RequiresVerbs) > 0) {
				ignored("nav.item", item.ID, "v2 item fields")
			}
			item.GroupRef = ""
			item.Parent = ""
			item.Page = ""
			item.Hidden = false
			item.Icon = ""
			item.Footer = false
			item.RequiresVerbs = nil
		}
		item.RequiresVerbs = unionVerbs(item.RequiresVerb, item.RequiresVerbs)
		if !verbsOK("nav.item", item.ID, item.RequiresVerbs) {
			continue
		}
		placements := 0
		for _, v := range []string{item.Group, item.GroupRef, item.Parent} {
			if v != "" {
				placements++
			}
		}
		if placements > 1 {
			add(NavPlacementConflict, "nav.item", item.ID, item.Route, "exactly one placement field allowed", false, true)
			continue
		}
		if item.Page != "" {
			p, ok := pages[item.Page]
			if !ok {
				add(NavPageMissing, "nav.item", item.ID, item.Route, "referenced page not admitted", false, true)
				continue
			}
			if item.Route != "" && item.Route != p.Route {
				add(NavRoutePageMismatch, "nav.item", item.ID, item.Route, "route assertion differs from page", false, true)
				continue
			}
			item.Route = p.Route
		} else {
			if item.Route == "" {
				add(NavPageMissing, "nav.item", item.ID, "", "no route or page", false, true)
				continue
			}
			if !routeOK(item.ID, item.Route) {
				continue
			}
			if pageID, exists := routes[item.Route]; exists {
				p := pages[pageID]
				if p.Synthesized {
					add(NavRouteCollision, "page", item.ID, item.Route, "synthesized route already admitted", false, true)
					continue
				}
				item.Page = pageID
			} else {
				if _, exists := pages[item.ID]; exists {
					add(NavDuplicateID, "page", item.ID, item.Route, "synthesized page id already admitted", false, true)
					continue
				}
				p := NavPage{ManifestOrder: i, ID: item.ID, Route: item.Route, Title: item.Label, View: "legacy-route:" + item.Route, RequiresVerbs: append([]string(nil), item.RequiresVerbs...), Synthesized: true, PluginID: pluginID}
				pages[p.ID] = p
				routes[p.Route] = p.ID
				out.Pages = append(out.Pages, p)
				item.Page = p.ID
			}
		}
		// Placement is resolved only after every plugin is admitted. A missing
		// local group is retained here and falls back to More in the projector.
		item.ManifestOrder = i
		item.PluginID = pluginID
		out.Items = append(out.Items, item)
	}
	if schema >= 2 {
		for i, s := range pc.Nav.Subnav {
			if invalid("subnav.item", i) || !idOK("subnav.item", s.ID) {
				continue
			}
			s.RequiresVerbs = unionVerbs("", s.RequiresVerbs)
			if !verbsOK("subnav.item", s.ID, s.RequiresVerbs) {
				continue
			}
			if s.Parent == "" || (s.Placement != "left" && s.Placement != "top") {
				add(NavMalformed, "subnav.item", s.ID, "", "subnav requires parent and left/top placement", false, true)
				continue
			}
			if _, ok := pages[s.Page]; !ok {
				add(NavPageMissing, "subnav.item", s.ID, "", "subnav page not admitted", false, true)
				continue
			}
			s.ManifestOrder = i
			s.PluginID = pluginID
			out.Subnav = append(out.Subnav, s)
		}
		for i, m := range pc.Nav.Menus {
			if invalid("menu.item", i) || !idOK("menu.item", m.ID) {
				continue
			}
			m.RequiresVerbs = unionVerbs("", m.RequiresVerbs)
			if !verbsOK("menu.item", m.ID, m.RequiresVerbs) {
				continue
			}
			// Typed intent/target/region policy remains the action gateway's job.
			m.Action = append(m.Action[:0:0], m.Action...)
			if m.Target != nil {
				target := *m.Target
				m.Target = &target
			}
			m.ManifestOrder = i
			m.PluginID = pluginID
			out.Menus = append(out.Menus, m)
		}
	}
	return out, diags
}

func unionVerbs(singular string, plural []string) []string {
	var out []string
	seen := map[string]bool{}
	add := func(v string) {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	if singular != "" {
		add(singular)
	}
	for _, v := range plural {
		add(v)
	}
	return out
}

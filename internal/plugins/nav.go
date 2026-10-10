package plugins

import (
	"fmt"
	"log/slog"
	"sort"

	"github.com/hollis-labs/tachyon/internal/contract"
)

func matchReservedRoute(route string) (string, bool) { return contract.ReservedNavRoute(route) }

// NavResolutionResult is a pure, revision-local host projection. Input declarations
// have passed NormalizeNav; neither process load order nor source attribution wins.
type NavResolutionResult struct {
	Nav                                              contract.NavDeclaration
	GroupOwners, ItemOwners, RouteOwners, RouteItems map[string]string
	Orders                                           map[string]int
	Placements                                       map[string]string
	DeclaredGroups                                   map[string]string
}

func navOrderKey(kind, owner, id string) string { return kind + "/" + owner + "/" + id }
func navPriority(p int) int {
	if p == 0 {
		return 1000
	}
	return p
}

// ResolveNavigation resolves all active declarations together, after local
// admission. loadOrder selects the active set only; plugin ID is the tie-break.
func ResolveNavigation(loadOrder []string, pluginNavs map[string]*contract.NavDeclaration, logger *slog.Logger) NavResolutionResult {
	r := NavResolutionResult{Nav: contract.NavDeclaration{Groups: []contract.NavGroup{}, Items: []contract.NavItem{}, Diagnostics: []contract.NavDiagnostic{}, Notices: []string{}}, GroupOwners: map[string]string{}, ItemOwners: map[string]string{}, RouteOwners: map[string]string{}, RouteItems: map[string]string{}, Orders: map[string]int{}, Placements: map[string]string{}, DeclaredGroups: map[string]string{}}
	diag := func(code, kind, owner, id, route, message string, dropped, info bool) {
		d := contract.NavDiagnostic{Reason: code, Kind: kind, PluginID: owner, ID: id, Route: route, Message: message, Dropped: dropped, Informational: info}
		if kind == "nav.group" {
			d.GroupID = id
		}
		if kind == "nav.item" {
			d.ItemID = id
		}
		r.Nav.Diagnostics = append(r.Nav.Diagnostics, d)
		r.Nav.Notices = append(r.Nav.Notices, message)
		if logger != nil {
			logger.Warn("navigation projection diagnostic", "plugin_id", owner, "kind", kind, "id", id, "reason", code, "dropped", dropped)
		}
	}
	ids := []string{}
	seen := map[string]bool{}
	for _, id := range loadOrder {
		if !seen[id] && pluginNavs[id] != nil {
			ids = append(ids, id)
			seen[id] = true
		}
	}
	sort.Strings(ids)
	groups := map[string]contract.NavGroup{}
	localGroups := map[string]map[string]bool{}
	pages := map[string]contract.NavPage{}
	items := map[string]contract.NavItem{}
	icon := func(kind, owner, id, value string) string {
		if value != "" && !contract.IsValidNavIcon(value) {
			diag(contract.NavIconUnknown, kind, owner, id, "", "unknown icon; using activity", false, true)
			return "activity"
		}
		return value
	}
	for _, owner := range ids {
		nav := pluginNavs[owner]
		localGroups[owner] = map[string]bool{}
		for _, d := range nav.Diagnostics {
			d.PluginID = owner
			r.Nav.Diagnostics = append(r.Nav.Diagnostics, d)
			r.Nav.Notices = append(r.Nav.Notices, d.Message)
		}
		for _, g := range nav.Groups {
			if owner == "core" || g.ID == "more" {
				diag(contract.NavReservedID, "nav.group", owner, g.ID, "", "reserved group identity", true, false)
				continue
			}
			localGroups[owner][g.ID] = true
			g.PluginID = owner
			g.Icon = icon("nav.group", owner, g.ID, g.Icon)
			r.Orders[navOrderKey("nav.group", owner, g.ID)] = g.ManifestOrder
			if old, exists := groups[g.ID]; exists {
				winner, loser := old.PluginID, owner
				if g.Owner && !old.Owner {
					groups[g.ID] = g
					winner, loser = owner, old.PluginID
				}
				diag(contract.NavGroupRedeclared, "nav.group", loser, g.ID, "", fmt.Sprintf("nav group collision %q: owner %q wins metadata over %q", g.ID, winner, loser), true, false)
			} else {
				groups[g.ID] = g
			}
		}
		sourcePages := append([]contract.NavPage(nil), nav.Pages...)
		// Legacy direct callers of this helper have not synthesized pages yet.
		if len(sourcePages) == 0 {
			for _, it := range nav.Items {
				if it.Page == "" && it.Route != "" {
					sourcePages = append(sourcePages, contract.NavPage{ID: it.ID, Route: it.Route, Title: it.Label, View: "legacy-route:" + it.Route, Synthesized: true})
				}
			}
		}
		for _, p := range sourcePages {
			p.PluginID = owner
			r.Orders[navOrderKey("page", owner, p.ID)] = p.ManifestOrder
			if !contract.ValidNavRoute(p.Route) {
				diag(contract.NavRouteInvalid, "page", owner, p.ID, p.Route, "invalid route", true, false)
				continue
			}
			if allowed, reserved := matchReservedRoute(p.Route); reserved && (allowed == "" || allowed != owner) {
				diag(contract.NavReservedRoute, "page", owner, p.ID, p.Route, "reserved host route", true, false)
				continue
			}
			if old, exists := pages[p.ID]; exists {
				diag(contract.NavIDCollision, "page", owner, p.ID, p.Route, "page id owned by "+old.PluginID, true, false)
				continue
			}
			if winner, exists := r.RouteOwners[p.Route]; exists {
				diag(contract.NavRouteCollision, "page", owner, p.ID, p.Route, "route owned by "+winner, true, false)
				continue
			}
			pages[p.ID] = p
			r.RouteOwners[p.Route] = owner
		}
	}
	// IDs are independent per kind; a dropped page never rebinds to another owner.
	for _, owner := range ids {
		for _, it := range pluginNavs[owner].Items {
			it.PluginID = owner
			it.Icon = icon("nav.item", owner, it.ID, it.Icon)
			r.Orders[navOrderKey("nav.item", owner, it.ID)] = it.ManifestOrder
			if old, exists := items[it.ID]; exists {
				diag(contract.NavIDCollision, "nav.item", owner, it.ID, it.Route, "nav item collision: id owned by "+old.PluginID, true, false)
				continue
			}
			pageID := it.Page
			if pageID == "" {
				pageID = it.ID
			}
			p, ok := pages[pageID]
			if !ok || p.PluginID != owner {
				diag(contract.NavPageMissing, "nav.item", owner, it.ID, it.Route, "page not admitted for owner", true, false)
				continue
			}
			it.Page = pageID
			it.Route = p.Route
			items[it.ID] = it
		}
	}
	// Groups may nest through group.parent; item.parent names an item. Both graph
	// walks are iterative: hostile sparse IDs or long chains cannot exhaust stack.
	groupDepth := map[string]int{}
	groupDrop := map[string]string{}
	groupParents := map[string]string{}
	for id, g := range groups {
		groupParents[id] = g.Parent
	}
	for id := range navCycleMembers(groupParents) {
		groupDrop[id] = contract.NavParentCycle
	}
	cycleGroups := map[string]bool{}
	for id, code := range groupDrop {
		cycleGroups[id] = code == contract.NavParentCycle
	}
	for _, id := range sortedNavKeys(groups) {
		if groupDrop[id] != "" {
			continue
		}
		depth := 0
		current := groups[id].Parent
		for current != "" {
			g, ok := groups[current]
			if !ok || cycleGroups[current] {
				break
			}
			depth++
			if depth > 2 {
				groupDrop[id] = contract.NavDepthExceeded
				break
			}
			current = g.Parent
		}
		groupDepth[id] = depth
	}
	for _, id := range sortedNavKeys(groups) {
		g := groups[id]
		if reason := groupDrop[id]; reason != "" {
			diag(reason, "nav.group", g.PluginID, id, "", "group topology exceeds bounds", true, false)
			delete(groups, id)
		}
	}
	for _, id := range sortedNavKeys(groups) {
		g := groups[id]
		if g.Parent != "" {
			if _, ok := groups[g.Parent]; !ok {
				diag(contract.NavOrphan, "nav.group", g.PluginID, id, "", "group parent absent; placed at top level", false, false)
				g.Parent = ""
				groups[id] = g
				groupDepth[id] = 0
			}
		}
	}
	itemDrop := map[string]string{}
	itemParents := map[string]string{}
	for id, it := range items {
		itemParents[id] = it.Parent
	}
	for id := range navCycleMembers(itemParents) {
		itemDrop[id] = contract.NavParentCycle
	}
	// Resolve survivors to a group without rewriting declaration input. Children of
	// refused/absent parents become top-level More, rather than inheriting authority.
	for _, id := range sortedNavKeys(items) {
		if itemDrop[id] != "" {
			continue
		}
		it := items[id]
		root := it
		depth := 1
		current := it.Parent
		for current != "" {
			parent, ok := items[current]
			if !ok || itemDrop[current] == contract.NavParentCycle {
				root = contract.NavItem{}
				break
			}
			depth++
			if depth > 2 {
				itemDrop[id] = contract.NavDepthExceeded
				break
			}
			root = parent
			current = parent.Parent
		}
		group := root.GroupRef
		if group == "" && localGroups[root.PluginID][root.Group] {
			group = root.Group
		}
		if _, ok := groups[group]; ok && depth+groupDepth[group] > 2 {
			itemDrop[id] = contract.NavDepthExceeded
		}
	}
	for _, id := range sortedNavKeys(items) {
		it := items[id]
		if reason := itemDrop[id]; reason != "" {
			diag(reason, "nav.item", it.PluginID, id, it.Route, "item topology exceeds bounds", true, false)
			delete(items, id)
		}
	}
	var resolveGroup func(string) string
	// Remaining chains are bounded; a parent can have been refused by depth check.
	resolveGroup = func(id string) string {
		it := items[id]
		if it.Parent != "" {
			if _, ok := items[it.Parent]; ok {
				return resolveGroup(it.Parent)
			}
			return "more"
		}
		if it.GroupRef != "" {
			if _, ok := groups[it.GroupRef]; ok {
				return it.GroupRef
			}
			return "more"
		}
		if localGroups[it.PluginID][it.Group] {
			if _, ok := groups[it.Group]; ok {
				return it.Group
			}
		}
		return "more"
	}
	more := false
	for _, id := range sortedNavKeys(items) {
		it := items[id]
		declared := it.Group
		placement := "declared"
		if it.GroupRef != "" {
			declared = it.GroupRef
			placement = "group_ref"
		}
		if it.Parent != "" {
			declared = it.Parent
			placement = "parent"
		}
		group := resolveGroup(id)
		if group == "more" {
			more = true
			placement = "orphan"
			it.Parent = ""
			diag(contract.NavOrphan, "nav.item", it.PluginID, id, it.Route, "placement target unavailable; placed in More", false, false)
		}
		it.Group = group
		r.Placements[id] = placement
		r.DeclaredGroups[id] = declared
		r.ItemOwners[id] = it.PluginID
		r.RouteItems[it.Route] = id
		r.Nav.Items = append(r.Nav.Items, it)
	}
	for _, id := range sortedNavKeys(groups) {
		g := groups[id]
		r.GroupOwners[id] = g.PluginID
		r.Nav.Groups = append(r.Nav.Groups, g)
	}
	if more {
		r.Nav.Groups = append(r.Nav.Groups, contract.NavGroup{ID: "more", Label: "More", Icon: "activity", PluginID: "core"})
	}
	for _, id := range sortedNavKeys(pages) {
		r.Nav.Pages = append(r.Nav.Pages, pages[id])
	}
	// Subnav pages are owner-local; parent items can come from another active plugin.
	subnavOwners := map[string]string{}
	menuOwners := map[string]string{}
	for _, owner := range ids {
		nav := pluginNavs[owner]
		for _, s := range nav.Subnav {
			s.PluginID = owner
			r.Orders[navOrderKey("subnav.item", owner, s.ID)] = s.ManifestOrder
			if winner, ok := subnavOwners[s.ID]; ok {
				diag(contract.NavIDCollision, "subnav.item", owner, s.ID, "", "subnav id owned by "+winner, true, false)
				continue
			}
			if _, ok := items[s.Parent]; !ok {
				diag(contract.NavSubnavOrphan, "subnav.item", owner, s.ID, "", "subnav parent unavailable", true, false)
				continue
			}
			p, ok := pages[s.Page]
			if !ok || p.PluginID != owner {
				diag(contract.NavPageMissing, "subnav.item", owner, s.ID, "", "subnav page unavailable", true, false)
				continue
			}
			subnavOwners[s.ID] = owner
			r.Nav.Subnav = append(r.Nav.Subnav, s)
		}
		for _, m := range nav.Menus {
			m.PluginID = owner
			r.Orders[navOrderKey("menu.item", owner, m.ID)] = m.ManifestOrder
			if winner, ok := menuOwners[m.ID]; ok {
				diag(contract.NavIDCollision, "menu.item", owner, m.ID, "", "menu id owned by "+winner, true, false)
				continue
			}
			// The 0119 gateway owns invocation. Only structurally valid typed intents
			// are projected here; never turn this declaration into execution authority.
			if !validNavMenu(m, owner, pages, r.RouteOwners) {
				diag(contract.NavMenuInvalidAction, "menu.item", owner, m.ID, "", "invalid menu intent or target", true, false)
				continue
			}
			m.Icon = icon("menu.item", owner, m.ID, m.Icon)
			menuOwners[m.ID] = owner
			r.Nav.Menus = append(r.Nav.Menus, m)
		}
	}
	sort.Slice(r.Nav.Groups, func(i, j int) bool {
		a, b := r.Nav.Groups[i], r.Nav.Groups[j]
		if a.ID == "more" || b.ID == "more" {
			return b.ID == "more" && a.ID != "more"
		}
		if navPriority(a.Priority) != navPriority(b.Priority) {
			return navPriority(a.Priority) < navPriority(b.Priority)
		}
		return a.ID < b.ID
	})
	sort.Slice(r.Nav.Items, func(i, j int) bool {
		a, b := r.Nav.Items[i], r.Nav.Items[j]
		if navPriority(a.Priority) != navPriority(b.Priority) {
			return navPriority(a.Priority) < navPriority(b.Priority)
		}
		return a.ID < b.ID
	})
	r.Nav.Tree = buildNavTree(r.Nav.Groups, r.Nav.Items)
	return r
}

// A functional parent graph is walked once. Sparse IDs are map keys, never
// array indexes; long hostile chains do not consume recursive stack or N² work.
func navCycleMembers(parents map[string]string) map[string]bool {
	cycles := map[string]bool{}
	done := map[string]bool{}
	for _, id := range sortedNavKeys(parents) {
		if done[id] {
			continue
		}
		chain := []string{}
		positions := map[string]int{}
		current := id
		for current != "" && !done[current] {
			if pos, ok := positions[current]; ok {
				for _, member := range chain[pos:] {
					cycles[member] = true
				}
				break
			}
			parent, ok := parents[current]
			if !ok {
				break
			}
			positions[current] = len(chain)
			chain = append(chain, current)
			current = parent
		}
		for _, member := range chain {
			done[member] = true
		}
	}
	return cycles
}

func sortedNavKeys[T any](values map[string]T) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func buildNavTree(groups []contract.NavGroup, items []contract.NavItem) []*contract.NavNode {
	nodes := map[string]*contract.NavNode{}
	roots := []*contract.NavNode{}
	for _, g := range groups {
		nodes["g/"+g.ID] = &contract.NavNode{Kind: "nav.group", ID: g.ID, Label: g.Label, Icon: g.Icon, Priority: navPriority(g.Priority), PluginID: g.PluginID, OwnerID: g.PluginID, Children: []*contract.NavNode{}}
	}
	for _, it := range items {
		nodes["i/"+it.ID] = &contract.NavNode{Kind: "nav.item", ID: it.ID, Label: it.Label, Icon: it.Icon, Route: it.Route, Page: it.Page, Hidden: it.Hidden, Priority: navPriority(it.Priority), PluginID: it.PluginID, OwnerID: it.PluginID, Children: []*contract.NavNode{}}
	}
	for _, g := range groups {
		n := nodes["g/"+g.ID]
		if parent := nodes["g/"+g.Parent]; parent != nil {
			parent.Children = append(parent.Children, n)
		} else {
			roots = append(roots, n)
		}
	}
	for _, it := range items {
		n := nodes["i/"+it.ID]
		parent := nodes["i/"+it.Parent]
		if parent == nil {
			parent = nodes["g/"+it.Group]
		}
		if parent != nil {
			parent.Children = append(parent.Children, n)
		}
	}
	var sortChildren func([]*contract.NavNode)
	sortChildren = func(nodes []*contract.NavNode) {
		sort.SliceStable(nodes, func(i, j int) bool {
			a, b := nodes[i], nodes[j]
			if a.ID == "more" && a.Kind == "nav.group" {
				return false
			}
			if b.ID == "more" && b.Kind == "nav.group" {
				return true
			}
			if a.Priority != b.Priority {
				return a.Priority < b.Priority
			}
			if a.Kind != b.Kind {
				return a.Kind < b.Kind
			}
			return a.ID < b.ID
		})
		for _, node := range nodes {
			sortChildren(node.Children)
		}
	}
	sortChildren(roots)
	return roots
}

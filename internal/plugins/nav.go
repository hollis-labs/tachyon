package plugins

import (
	"fmt"
	"log/slog"
	"sort"

	"github.com/hollis-labs/tachyon/internal/contract"
)

// matchReservedRoute delegates to the contract's shared DEC081 allowlist.
func matchReservedRoute(route string) (allowedOwner string, isReserved bool) {
	return contract.ReservedNavRoute(route)
}

// NavResolutionResult holds the output of resolving navigation declarations
// across loaded plugins, including attribution, admitted groups/items,
// collision tracking, and visible diagnostics.
type NavResolutionResult struct {
	Nav         contract.NavDeclaration
	GroupOwners map[string]string // groupID -> winning pluginID
	ItemOwners  map[string]string // itemID -> winning pluginID
	RouteOwners map[string]string // route -> winning pluginID
	RouteItems  map[string]string // route -> winning itemID
}

// ResolveNavigation resolves navigation declarations across plugins deterministically
// in loadOrder. It enforces reserved host routes, duplicate item/route rejection,
// host-added plugin_id attribution, bounded diagnostics, and icon vocabulary warnings.
func ResolveNavigation(
	loadOrder []string,
	pluginNavs map[string]*contract.NavDeclaration,
	logger *slog.Logger,
) NavResolutionResult {
	result := NavResolutionResult{
		Nav: contract.NavDeclaration{
			Groups:      []contract.NavGroup{},
			Items:       []contract.NavItem{},
			Diagnostics: []contract.NavDiagnostic{},
			Notices:     []string{},
		},
		GroupOwners: make(map[string]string),
		ItemOwners:  make(map[string]string),
		RouteOwners: make(map[string]string),
		RouteItems:  make(map[string]string),
	}

	addDiag := func(diag contract.NavDiagnostic) {
		result.Nav.Diagnostics = append(result.Nav.Diagnostics, diag)
		result.Nav.Notices = append(result.Nav.Notices, diag.Message)
	}

	// 1. Process groups and items in deterministic load order
	for _, pluginID := range loadOrder {
		nav := pluginNavs[pluginID]
		if nav == nil {
			continue
		}

		// These diagnostics are host-produced by NormalizeNav before resolution.
		for _, diag := range nav.Diagnostics {
			addDiag(diag)
		}

		// Groups
		for _, g := range nav.Groups {
			// Check icon vocabulary
			if g.Icon != "" && !contract.IsValidNavIcon(g.Icon) {
				msg := fmt.Sprintf("nav group %q from plugin %q specifies unknown icon %q; will fall back to activity", g.ID, pluginID, g.Icon)
				if logger != nil {
					logger.Warn("unknown nav icon; falling back to activity", "group_id", g.ID, "plugin_id", pluginID, "icon", g.Icon)
				}
				addDiag(contract.NavDiagnostic{
					Reason:   "unknown_icon",
					Message:  msg,
					PluginID: pluginID,
					GroupID:  g.ID,
				})
			}

			// Group ID collision
			if winner, exists := result.GroupOwners[g.ID]; exists {
				msg := fmt.Sprintf("nav group collision for %q: plugin %q wins, dropped declaration from %q", g.ID, winner, pluginID)
				if logger != nil {
					logger.Warn("nav group collision; first loaded declaration wins", "group_id", g.ID, "winner", winner, "loser", pluginID)
				}
				addDiag(contract.NavDiagnostic{
					Reason:   "group_collision",
					Message:  msg,
					PluginID: pluginID,
					GroupID:  g.ID,
				})
				continue
			}

			result.GroupOwners[g.ID] = pluginID
			groupCopy := g
			groupCopy.PluginID = pluginID
			result.Nav.Groups = append(result.Nav.Groups, groupCopy)
		}

		// Items
		for _, item := range nav.Items {
			// Check reserved host routes (only for non-empty routes)
			if item.Route != "" {
				if allowedOwner, isReserved := matchReservedRoute(item.Route); isReserved {
					if allowedOwner == "" || allowedOwner != pluginID {
						msg := fmt.Sprintf("plugin %q attempted to claim reserved host route %q (item %q); dropped", pluginID, item.Route, item.ID)
						if logger != nil {
							logger.Warn("plugin claimed reserved host route; dropped", "plugin_id", pluginID, "route", item.Route, "item_id", item.ID)
						}
						addDiag(contract.NavDiagnostic{
							Reason:   "reserved_route",
							Message:  msg,
							PluginID: pluginID,
							Route:    item.Route,
							ItemID:   item.ID,
						})
						continue
					}
				}
			}

			// Check duplicate item ID
			if winner, exists := result.ItemOwners[item.ID]; exists {
				msg := fmt.Sprintf("nav item collision for %q: plugin %q wins, dropped declaration from %q", item.ID, winner, pluginID)
				if logger != nil {
					logger.Warn("nav item collision; first loaded declaration wins", "item_id", item.ID, "winner", winner, "loser", pluginID)
				}
				addDiag(contract.NavDiagnostic{
					Reason:   "item_collision",
					Message:  msg,
					PluginID: pluginID,
					ItemID:   item.ID,
				})
				continue
			}

			// Check duplicate route (only for non-empty routes)
			if item.Route != "" {
				if winner, exists := result.RouteOwners[item.Route]; exists {
					winningItem := result.RouteItems[item.Route]
					msg := fmt.Sprintf("nav route collision for %q: item %q from plugin %q wins, dropped item %q from plugin %q", item.Route, winningItem, winner, item.ID, pluginID)
					if logger != nil {
						logger.Warn("nav route collision; first loaded route wins", "route", item.Route, "winner_plugin", winner, "winner_item", winningItem, "loser_plugin", pluginID, "loser_item", item.ID)
					}
					addDiag(contract.NavDiagnostic{
						Reason:   "route_collision",
						Message:  msg,
						PluginID: pluginID,
						Route:    item.Route,
						ItemID:   item.ID,
					})
					continue
				}
			}

			// Admitted item
			result.ItemOwners[item.ID] = pluginID
			if item.Route != "" {
				result.RouteOwners[item.Route] = pluginID
				result.RouteItems[item.Route] = item.ID
			}

			itemCopy := item
			itemCopy.PluginID = pluginID
			result.Nav.Items = append(result.Nav.Items, itemCopy)
		}
	}

	// 2. Deterministic sorting: priority ascending, then ID ascending
	priorityVal := func(p int) int {
		if p == 0 {
			return 1000
		}
		return p
	}

	sort.Slice(result.Nav.Groups, func(i, j int) bool {
		a, b := result.Nav.Groups[i], result.Nav.Groups[j]
		pa, pb := priorityVal(a.Priority), priorityVal(b.Priority)
		if pa == pb {
			return a.ID < b.ID
		}
		return pa < pb
	})

	sort.Slice(result.Nav.Items, func(i, j int) bool {
		a, b := result.Nav.Items[i], result.Nav.Items[j]
		pa, pb := priorityVal(a.Priority), priorityVal(b.Priority)
		if pa == pb {
			return a.ID < b.ID
		}
		return pa < pb
	})

	return result
}

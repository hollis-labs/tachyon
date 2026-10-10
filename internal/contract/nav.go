package contract

// NavGroup declares a navigation group that appears in the Sysop UI's
// side rail. Plugins declare nav groups in their capabilities; the host
// merges them by ID and the browser-side loader renders the merged tree.
// User configuration can override label, priority, and visibility.
type NavGroup struct {
	// ID is the unique identifier for this nav group. Must be globally
	// unique across all plugins — collisions are resolved by the first
	// successfully loaded plugin to claim the ID.
	ID string `json:"id"`

	// Label is the display name shown in the nav rail.
	Label string `json:"label"`

	// Icon is the icon identifier (from the host's icon set).
	Icon string `json:"icon,omitempty"`

	// Priority controls ordering in the nav rail. Lower values appear
	// first. Default is 1000.
	Priority int `json:"priority,omitempty"`

	// PluginID is the host-attributed identifier of the plugin owning this group.
	PluginID string `json:"plugin_id,omitempty"`
}

// NavItem declares a single navigable view within a nav group. Each
// item maps to a route in the frontend and is visibility-gated on
// a required verb from the plugin's capability declaration.
type NavItem struct {
	// ID is the unique identifier for this nav item.
	ID string `json:"id"`

	// Label is the display text for the nav item.
	Label string `json:"label"`

	// Group is the ID of the NavGroup this item belongs to.
	Group string `json:"group"`

	// Route is the frontend route path (e.g. "/agents", "/agents/durable").
	Route string `json:"route"`

	// RequiresVerb gates visibility: the item is only shown when this
	// verb is present in the owning plugin's capability declaration.
	// Empty means always visible.
	RequiresVerb string `json:"requires_verb,omitempty"`

	// Priority controls ordering within the group. Lower values appear
	// first. Default is 1000.
	Priority int `json:"priority,omitempty"`

	// PluginID is the host-attributed identifier of the plugin that contributed this item.
	PluginID string `json:"plugin_id,omitempty"`
}

// NavDiagnostic records an issue encountered during navigation resolution,
// such as a route collision, duplicate item ID, or reserved route claim.
type NavDiagnostic struct {
	Reason   string `json:"reason"`
	Message  string `json:"message"`
	PluginID string `json:"plugin_id,omitempty"`
	Route    string `json:"route,omitempty"`
	ItemID   string `json:"item_id,omitempty"`
	GroupID  string `json:"group_id,omitempty"`
}

// Each item references a group declared by the same plugin. Plugins may
// declare the same group ID to contribute items to a shared group; the host
// keeps the first-loaded group metadata.
//
// NavDeclaration is the nav contribution a plugin includes in its
// capability declaration. The host merges declarations from all
// loaded plugins into a single nav tree for the browser loader.
type NavDeclaration struct {
	Groups      []NavGroup      `json:"groups,omitempty"`
	Items       []NavItem       `json:"items,omitempty"`
	Diagnostics []NavDiagnostic `json:"diagnostics,omitempty"`
	Notices     []string        `json:"notices,omitempty"`
}

// ValidNavIcons defines the set of documented valid navigation icon identifiers.
var ValidNavIcons = map[string]bool{
	"activity":         true,
	"clipboard":        true,
	"clipboard-list":   true,
	"dashboard":        true,
	"git":              true,
	"git-branch":       true,
	"layout-dashboard": true,
	"play":             true,
	"radio":            true,
	"rocket":           true,
	"server":           true,
	"settings":         true,
	"terminal":         true,
	"users":            true,
}

// IsValidNavIcon returns whether an icon identifier is recognized by the host.
func IsValidNavIcon(icon string) bool {
	return ValidNavIcons[icon]
}

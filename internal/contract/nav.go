package contract

import "encoding/json"

// NavGroup declares source-owned group metadata. The projector resolves owner
// hints across plugins; PluginID is always attributed by the host.
type NavGroup struct {
	ID        string `json:"id"`
	Label     string `json:"label"`
	Icon      string `json:"icon,omitempty"`
	Priority  int    `json:"priority,omitempty"`
	Parent    string `json:"parent,omitempty"`
	Kind      string `json:"kind,omitempty"`
	Collapsed bool   `json:"collapsed,omitempty"`
	Footer    bool   `json:"footer,omitempty"`
	Owner     bool   `json:"owner,omitempty"`
	PluginID  string `json:"plugin_id,omitempty"`
}

// NavItem authors a navigation entry. Page is authoritative; Route, when both
// exist, is a consistency assertion. Required verbs are an all-of union.
type NavItem struct {
	ID            string   `json:"id"`
	Label         string   `json:"label"`
	Group         string   `json:"group,omitempty"`
	Route         string   `json:"route,omitempty"`
	RequiresVerb  string   `json:"requires_verb,omitempty"`
	Priority      int      `json:"priority,omitempty"`
	GroupRef      string   `json:"group_ref,omitempty"`
	Parent        string   `json:"parent,omitempty"`
	Page          string   `json:"page,omitempty"`
	Hidden        bool     `json:"hidden,omitempty"`
	Icon          string   `json:"icon,omitempty"`
	Footer        bool     `json:"footer,omitempty"`
	RequiresVerbs []string `json:"requires_verbs,omitempty"`
	PluginID      string   `json:"plugin_id,omitempty"`
}

// NavPage registers a route independently of navigation visibility. View names
// a host-compiled component in wave 1; it cannot authorize plugin JavaScript.
type NavPage struct {
	ID            string   `json:"id"`
	Route         string   `json:"route"`
	Title         string   `json:"title"`
	View          string   `json:"view"`
	Hidden        bool     `json:"hidden,omitempty"`
	RequiresVerbs []string `json:"requires_verbs,omitempty"`
	Synthesized   bool     `json:"synthesized,omitempty"`
	PluginID      string   `json:"plugin_id,omitempty"`
}

type NavSubnav struct {
	ID            string   `json:"id"`
	Label         string   `json:"label"`
	Parent        string   `json:"parent"`
	Page          string   `json:"page"`
	Placement     string   `json:"placement"`
	Priority      int      `json:"priority,omitempty"`
	RequiresVerbs []string `json:"requires_verbs,omitempty"`
	PluginID      string   `json:"plugin_id,omitempty"`
}

// NavMenu carries a declarative intent, not executable code. Action gateway
// validation and invocation are owned by the menu implementation (0119).
type NavMenu struct {
	ID            string          `json:"id"`
	Label         string          `json:"label"`
	Region        string          `json:"region"`
	Target        *NavMenuTarget  `json:"target,omitempty"`
	Priority      int             `json:"priority,omitempty"`
	Icon          string          `json:"icon,omitempty"`
	RequiresVerbs []string        `json:"requires_verbs,omitempty"`
	Action        json.RawMessage `json:"action"`
	PluginID      string          `json:"plugin_id,omitempty"`
}

type NavMenuTarget struct {
	Page string `json:"page,omitempty"`
	Row  string `json:"row,omitempty"`
}

// NavDiagnostic is host-produced. Kind/ID identify the owned blast radius;
// informational diagnostics never imply a drop. Plugins cannot mint refusals.
type NavDiagnostic struct {
	Reason        string `json:"reason"`
	Message       string `json:"message"`
	PluginID      string `json:"plugin_id,omitempty"`
	Route         string `json:"route,omitempty"`
	ItemID        string `json:"item_id,omitempty"`
	GroupID       string `json:"group_id,omitempty"`
	Kind          string `json:"kind,omitempty"`
	ID            string `json:"id,omitempty"`
	Informational bool   `json:"informational,omitempty"`
	Dropped       bool   `json:"dropped,omitempty"`
}

// NavDeclaration is authored solely in capabilities.json and transported over
// plugin_capabilities / command/execute protocol 2. Schema is a sibling field
// on PluginCapabilities (absent/0 = legacy schema 1; schema 2 enables additions).
// Schema-1 v2 fields are ignored with informational nav-field-ignored diagnostics;
// newer schemas use supported schema-2 fields with one nav-schema-newer notice.
// Decoding is lenient about unknown future keys, which are discarded on the
// struct carrier's re-encode. Known malformed nav fields are retained through
// that carrier and dropped only in NormalizeNav, so valid non-nav registration
// survives. Diagnostics and Notices are host-only output fields; normalization
// ignores source-supplied values. Neither nav_schema nor this carrier changes
// subprocess protocol 2.
type NavDeclaration struct {
	malformed    json.RawMessage
	decodeIssues []navDecodeIssue
	presence     []navFieldPresence
	Groups       []NavGroup      `json:"groups,omitempty"`
	Items        []NavItem       `json:"items,omitempty"`
	Pages        []NavPage       `json:"pages,omitempty"`
	Subnav       []NavSubnav     `json:"subnav,omitempty"`
	Menus        []NavMenu       `json:"menus,omitempty"`
	Diagnostics  []NavDiagnostic `json:"diagnostics,omitempty"`
	Notices      []string        `json:"notices,omitempty"`
}

// NavProjection is an additive registry signal. Its runtime production belongs
// to the projector. Empty catalogs are valid and must not imply failure.
type NavProjection struct {
	Status   NavProjectionStatus `json:"status"`
	Revision int64               `json:"revision"`
	Dropped  int                 `json:"dropped,omitempty"`
	Reason   string              `json:"reason,omitempty"`
}
type NavProjectionStatus string

const (
	NavProjectionOK       NavProjectionStatus = "ok"
	NavProjectionDegraded NavProjectionStatus = "degraded"
	NavProjectionFailed   NavProjectionStatus = "failed"
)

// ValidNavIcons is the bounded host vocabulary; unknown names use activity.
var ValidNavIcons = map[string]bool{
	"activity": true, "clipboard": true, "clipboard-list": true, "dashboard": true,
	"git": true, "git-branch": true, "layout-dashboard": true, "play": true, "radio": true,
	"rocket": true, "server": true, "settings": true, "terminal": true, "users": true,
}

func IsValidNavIcon(icon string) bool { return ValidNavIcons[icon] }

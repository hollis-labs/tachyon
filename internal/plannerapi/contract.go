// Package plannerapi is the contract for the Planner backend extension (typed item links, pin,
// area/horizon, assignments, Torque-backed task lanes, timeline, momentum, need-you) and a
// fixture-backed stub of it for GUI development (TACHYON_PLANNER_STUBS=1). The real planner is
// internal/planner; PL1-PL5 implement these routes there. Design: Tesseract
// design_tachyon_planner_backend_20261030, sections 3 and 6.
//
// Only routes that do not exist in the real planner are served by the stub, plus the extended
// comment route and the idea promote route; the extended shapes of existing routes (list, get, patch,
// board) are served as named fixtures under GET /stub-fixtures/{name}. The stub never touches the
// planner store.
package plannerapi

// Prefix is the same prefix the real planner serves. Each stub pattern is more specific than the
// real planner's subtree mount, so the stub wins without a duplicate registration.
const Prefix = "/api/plugins/console/v1/planner"

// Routes are relative to Prefix. Reads and writes; writes are checked for shape only and change no state
// except through the X-Stub-Scenario control.
var Routes = []string{
	// items
	"POST /{db}/{id}/comments", // extended: status, pin, mentions
	"PUT /{db}/{id}/pin",       // toggle; item payloads name the revision `rev`, request bodies keep expected_revision
	"POST /{db}/{id}/relations", "DELETE /{db}/{id}/relations", "GET /{db}/{id}/links",
	"POST /ideas/{id}/promote",
	// tasks (Torque-backed)
	"GET /themes/{id}/tasks", "POST /tasks/{task}/transition", "PUT /tasks/{task}/meta", "GET /task-context",
	// assignments
	"POST /assignments", "GET /assignments", "POST /assignments/{id}/complete", "POST /assignments/{id}/cancel",
	"GET /agents/{agent_id}/work",
	// timeline, momentum, need-you
	"GET /themes/{id}/timeline", "GET /workstreams/{id}/timeline", "GET /momentum", "GET /need-you",
	// stub only
	"GET /stub/stream", "GET /stub-fixtures/{name}", "GET /stub/openapi.yaml",
}

// Databases (the {db} segment); themes is stored as priorities.
var Databases = []string{"themes", "workstreams", "roadmap", "ideas", "decisions", "risks", "inbox"}

// Relation kinds and the rules the real writer enforces.
const (
	RelNeeds     = "needs"     // same DB, no cycles
	RelDelivers  = "delivers"  // theme -> roadmap only
	RelMitigates = "mitigates" // theme -> risk only
	RelRelates   = "relates"
)

var RelationKinds = []string{RelNeeds, RelDelivers, RelMitigates, RelRelates}

// Lanes of a theme's task board; Torque status maps onto them.
var Lanes = []string{"todo", "doing", "review", "blocked", "done"}

// Horizons and the theme area enum (areas match the roadmap enum).
var (
	Horizons = []string{"now", "next", "later", "someday"}
	Areas    = []string{"platform", "agents", "planner", "chat", "forge", "settings", "other"}
)

// Momentum labels and need-you reasons.
var (
	MomentumLabels = []string{"rising", "steady", "cooling", "stalled"}
	NeedYouReasons = []string{"review", "decide", "blocked", "stale"}
)

// Timeline event kinds and facets.
var (
	EventKinds = []string{"task", "pr", "decision", "blocker", "comment", "workstream", "lifecycle", "link"}
	Facets     = []string{"all", "tasks", "prs", "decisions", "blockers", "comments", "workstreams"}
)

// Milestone keys and states.
var (
	MilestoneKeys   = []string{"approved", "first_done", "half_done"}
	MilestoneStates = []string{"reached", "next", "pending"}
)

// Assignment states; why values of /agents/{id}/work.
var (
	AssignmentStates = []string{"active", "completed", "cancelled"}
	WorkWhy          = []string{"assigned", "session", "referenced"}
)

// Stream event types (SSE). planner.changed stays for compatibility.
var StreamEvents = []string{"planner.changed", "planner.item", "planner.event", "planner.task", "planner.need_you", "resync"}

// Limits.
const (
	MaxCommentBytes   = 16 << 10
	MaxSummaryChars   = 600
	MaxRelationsItem  = 200
	DefaultTimeline   = 30
	MaxTimeline       = 100
	DefaultStaleDays  = 7
	RetentionDays     = 180
	UndoWindowSeconds = 8 // client-held: the undo of a lane move is the inverse transition
)

// Fixture ids the stub knows.
const (
	ThemeRising  = "theme-agents-console"  // rising, pinned, assigned, need-you review+decide
	ThemeSteady  = "theme-forge-gui"       // steady
	ThemeCooling = "theme-transcripts"     // cooling, stale
	ThemeStalled = "theme-settings-center" // stalled, blocked
	ThemeUnknown = "theme-unknown"         // 404
	TaskBlocked  = "CW-20261030-0900"      // transition 409 / needs reason demo
)

// Guard: the real routes stay behind the existing planner write path (operator.RequireOperator).
// New writes call the assurance Provider requirements table; there are no new guards. See
// docs/planner-contract.md.
const GuardRequirement = "planner.write"

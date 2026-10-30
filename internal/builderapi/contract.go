// Package builderapi is the contract for Tachyon's Builder (Agents, Launches,
// Library) and Registry (Projects) backend: the route list, the entity shapes,
// the change state machine and the stream envelope, plus a fixture-backed stub
// the Builder GUI builds against (TACHYON_BUILDER_STUBS=1). Real slices replace
// the stub route by route; the shapes in openapi.yaml are the final ones.
//
// The Tether catalog is the system of record. Tachyon is a typed editor and
// read model over it: every write is a change that becomes a Tether plan, and
// plan approval stays with the operator on the host (see docs/builder-contract.md).
package builderapi

import "regexp"

// Root is the console API root both prefixes hang off. It must stay inside the
// operator session cookie's path (approvals.CookiePath).
const Root = "/api/plugins/console/v1"

// Prefix is where Builder routes mount; RegistryPrefix is where the Registry
// project routes mount.
const (
	Prefix         = Root + "/builder"
	RegistryPrefix = Root + "/registry"
)

// Library kinds (the {kind} path segment).
const (
	LibPrompts     = "prompts"
	LibSkills      = "skills"
	LibToolServers = "tool_servers"
	LibPolicies    = "policies"
)

// LibraryKinds lists the valid {kind} values.
var LibraryKinds = []string{LibPrompts, LibSkills, LibToolServers, LibPolicies}

// Agent states. retired has no native Tether representation yet (design F4):
// Summary.Capabilities.Retired says whether it can be set.
const (
	AgentActive   = "active"
	AgentDisabled = "disabled"
	AgentRetired  = "retired"
)

// Change modes (GET Prefix/capabilities changes.mode). direct: Tachyon holds a control token, plans and applies
// itself (the original B3 flow). handoff: no standing catalog.write; Tachyon prepares a bundle the operator (or
// a session) plans in Tether, and DETECTS the apply by file shas. unavailable: changes are off (503
// changes_unavailable). HandoffCopy is the always-available handoff; HandoffSession launches an applier session.
const (
	ModeDirect      = "direct"
	ModeHandoff     = "handoff"
	ModeUnavailable = "unavailable"
	HandoffCopy     = "copy"
	HandoffSession  = "session"
)

// Change states. A change is one edit to the catalog; it moves
// draft -> awaiting_approval -> approved -> applying -> applied (direct mode), or
// draft -> awaiting_handoff -> [awaiting_approval] -> applied (handoff mode), or ends in
// failed, rolled_back, stale, expired or cancelled.
const (
	ChangeDraft            = "draft"
	ChangeAwaitingHandoff  = "awaiting_handoff" // handoff mode: the bundle is ready and nobody has planned it in Tether yet
	ChangeAwaitingApproval = "awaiting_approval"
	ChangeApproved         = "approved"
	ChangeApplying         = "applying"
	ChangeApplied          = "applied"
	ChangeFailed           = "failed" // carries Failure.Step
	ChangeRolledBack       = "rolled_back"
	ChangeStale            = "stale" // a base sha moved; carries Stale{mine_diff, theirs_diff}
	ChangeExpired          = "expired"
	ChangeCancelled        = "cancelled"
)

// ChangeStates lists every state a change fixture must cover.
var ChangeStates = []string{ChangeDraft, ChangeAwaitingHandoff, ChangeAwaitingApproval, ChangeApproved, ChangeApplying, ChangeApplied,
	ChangeFailed, ChangeRolledBack, ChangeStale, ChangeExpired, ChangeCancelled}

// Open reports whether a change still holds its target paths: only one open
// change may exist per path (409 change_open otherwise).
func Open(state string) bool {
	switch state {
	case ChangeDraft, ChangeAwaitingHandoff, ChangeAwaitingApproval, ChangeApproved, ChangeApplying, ChangeStale:
		return true
	}
	return false
}

// Failure steps.
const (
	StepValidate = "validate"
	StepPlan     = "plan"
	StepApply    = "apply"
	StepReload   = "reload"
)

// Edit operation types (ops[].op.type). Delete is not exposed in v1; a Library
// part is retired with archive. restore_version sets a file to a snapshot's
// content and never restores env values.
const (
	OpSet            = "set"
	OpAppend         = "append"
	OpRemove         = "remove"
	OpCreate         = "create"
	OpRestoreVersion = "restore_version"
	OpArchive        = "archive" // Library parts only
)

// Change target kinds. A part target also carries part_kind (prompt|policy).
const (
	TargetAgent   = "agent"
	TargetLaunch  = "launch"
	TargetProject = "project"
	TargetSkill   = "skill"
	TargetPart    = "part"
)

// How a Resolved card was computed.
const (
	ResolvedByTether = "tether"
	ResolvedByApprox = "tachyon_approx"
)

// Check statuses.
const (
	CheckOK   = "ok"
	CheckWarn = "warn"
	CheckFail = "fail"
)

// Stream event types on GET Prefix/stream. Flat JSON {type, at, ...}.
const (
	EventChange  = "change"  // {change_id, state}
	EventCatalog = "catalog" // {observed_at, changed_paths[], source}
	EventSummary = "summary" // the Summary counts
)

// IDed reports whether a stream event type is replayable: it carries an SSE
// `id:` and is replayed after Last-Event-ID. Summary is a live snapshot; on a
// reconnect replay cannot cover, the client refetches GET Prefix/summary.
func IDed(t string) bool { return t == EventChange || t == EventCatalog }

// Limits.
const (
	PageSize         = 25
	MaxOps           = 20
	MaxNoteChars     = 500
	MaxPromptBytes   = 64 << 10  // a Library prompt
	MaxFileBytes     = 256 << 10 // a catalog file (Tether's limit)
	ChangesPerMinute = 10        // per operator
	PlanTTLSeconds   = 15 * 60   // a Tether plan; the same bundle re-arms the same plan_id
)

// IDPattern is the slug every entity id (and every created id) must match.
// Paths are derived from ids server-side and never accepted from a client.
var IDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

// ProjectWriteFields is the allowlist of registry project fields a client may
// write; null and unknown fields are rejected.
var ProjectWriteFields = []string{"display_name", "description", "tags", "guidelines", "entry_points", "status"}

// FieldPath is the shape of an edit op path. Paths address the editor model
// returned by GET agents/{id} (for example policy.permission_mode or
// tools_skills.skills); the server maps them onto YAML fields and never
// accepts a file path.
var FieldPath = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(\.[A-Za-z_][A-Za-z0-9_-]*)*$`)

// PrivilegedPath reports whether editing the path changes agent authority and
// so needs ack_privileged on submit.
func PrivilegedPath(p string) bool {
	switch p {
	case "policy.permission_mode", "policy.network", "policy.default_sandbox", "policy.mcp_scopes",
		"tools_skills.mcp_servers", "runtime.extends":
		return true
	}
	return false
}

// MarkerOpen and MarkerClose delimit a compiled Library prompt inside an agent
// body. Text a client sends must never contain either, so a prompt cannot forge
// a marker block.
const (
	MarkerOpen  = "<!-- tachyon:prompt"
	MarkerClose = "<!-- /tachyon:prompt"
)

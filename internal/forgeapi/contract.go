// Package forgeapi is the contract for Tachyon's Pull requests backend (the Repos nav):
// the pending inbox, the PR workspace reads, merge preflight and guarded merge,
// review validation, server-side agent assignment, and the shared triage state.
// The stub (TACHYON_FORGE_STUBS=1) serves fixtures so the GUI builds against the
// final shapes; later slices replace routes one by one. The routes that exist today
// (plugins/console/forge.go) keep their paths and camelCase shapes; new routes and
// new fields are snake_case with unix-ms times. Design: tesseract key
// design_tachyon_pull_requests_backend_20261030.
package forgeapi

import (
	"fmt"
	"regexp"
	"strings"
)

// Prefix is the forge route family; StatePrefix the shared item-state store the console UI already calls.
const (
	Prefix      = "/api/plugins/console/v1/forge"
	StatePrefix = "/api/plugins/console/v1/console/state"
)

// Reasons a PR is in the pending inbox.
var Reasons = []string{"checks_failed", "changes_requested", "conflicting", "review_requested", "mentioned", "unassigned", "stale"}

// Merge preflight state and blocker codes.
var PreflightStates = []string{"ready", "blocked", "pending", "unknown"}
var Blockers = []string{"draft", "closed", "conflicts", "checks_failing", "checks_pending", "changes_requested", "review_required", "behind_base", "branch_protection", "head_moved", "unknown_mergeable"}

// Merge methods and review verdicts.
var Methods = []string{"merge", "squash", "rebase"}
var Verdicts = []string{"approve", "request_changes", "comment"}

// Triage (shared item state) statuses and the operations a client may send.
// The existing UI store uses unread|read|dismissed|snoozed|settled; reopen returns to unread.
var TriageStatuses = []string{"unread", "read", "dismissed", "snoozed", "settled"}
var TriageOps = []string{"read", "dismiss", "snooze", "settle", "reopen"}

// Agent link tasks and statuses (the assign-agent state machine).
var Tasks = []string{"review", "fix_checks", "address_comments", "resolve_conflicts"}
var LinkStatuses = []string{"pending", "assigned", "sent", "done", "failed", "cancelled"}

// Audit ops and outcomes (forge_write_log). Bodies are never logged.
var AuditOps = []string{"comment", "review", "merge", "assign", "assign_agent", "triage"}
var AuditOutcomes = []string{"ok", "refused", "error"}

// SSE event types on GET /stream. Ids are a replayable sequence; a gap yields resync.
var StreamEvents = []string{"pr.updated", "pr.merged", "triage.changed", "status", "resync"}

// Alert-feed event kinds produced in-process by the poller (not SSE; the Alerts ingest consumes them).
var FeedKinds = []string{"pr.review_requested", "pr.checks_failed", "pr.changes_requested", "pr.conflicting", "pr.stale", "pr.merged"}

// Error codes a client can branch on (HTTP status in brackets).
var ErrorCodes = []string{
	"head_moved",               // 409, the PR head differs from the pinned head_sha
	"merge_blocked",            // 409, carries blockers
	"idempotency_conflict",     // 409, same key with a different body
	"idempotency_key_required", // 400
	"conflict",                 // 409, GitHub declined the merge
	"rate_limited",             // 429, carries retry_after_s
	"repo_not_allowed",         // 403, repo outside the watched set
	"operator_access_required", // 403
	"requester_cannot_approve", // 403, Authorization header on a write
	"origin_denied",            // 403
	"session_required",         // 401
	"credential_missing",       // 401, no GitHub token configured
	"agent_not_found",          // 404
	"agent_not_addressable",    // 422, no deliverable Tether URN
	"pr_not_open",              // 409
	"invalid_args",             // 400
	"not_found",                // 404
	"unavailable",              // 501/502, other hosts, upstream
}

// Limits (design section 7).
const (
	MaxCommentBytes    = 64 << 10
	MaxReviewBytes     = 256 << 10
	MaxInlineComments  = 200
	MaxPendingDefault  = 100
	PendingPage        = 25
	IdempotencyTTLH    = 24
	DiffTruncateBytes  = 200 << 10
	StaleDaysDefault   = 7
	PollSecondsDefault = 60
	RateFloorDefault   = 200
)

// WriteLevels is the assurance table for forge writes, keyed by audit op, using the
// assurance package's level names. P1 is decided (addendum1): forge.merge is
// browser-session, only the operator merges from the UI, and agents get no forge
// writes (an Authorization header on a write is refused).
var WriteLevels = map[string]string{
	"comment":      "browser-session",
	"review":       "browser-session",
	"merge":        "browser-session",
	"assign":       "browser-session",
	"assign_agent": "browser-session",
	"triage":       "browser-session",
}

// StepUpLevels is the reserved mapping that makes a step-up swap a one-line change:
// set StepUpEnabled and RequiredLevel returns these instead of WriteLevels. Off.
var StepUpLevels = map[string]string{"merge": "operator-token"}

// StepUpEnabled switches RequiredLevel to StepUpLevels for the ops it names.
var StepUpEnabled = false

// RequiredLevel is the assurance level an op needs right now.
func RequiredLevel(op string) string {
	if StepUpEnabled {
		if l, ok := StepUpLevels[op]; ok {
			return l
		}
	}
	return WriteLevels[op]
}

var (
	ownerName  = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)
	headSHA    = regexp.MustCompile(`^[0-9a-f]{40}$`)
	prKeyShape = regexp.MustCompile(`^github:([A-Za-z0-9_.-]+)/([A-Za-z0-9_.-]+)#([1-9][0-9]*)$`)
)

// ValidRepoPart reports whether an owner or repo name is safe to use in a path.
func ValidRepoPart(s string) bool {
	return s != "" && len(s) <= 100 && s != "." && s != ".." && ownerName.MatchString(s)
}

// ValidHeadSHA reports a full 40-hex commit id, which merge and review pins require.
func ValidHeadSHA(s string) bool { return headSHA.MatchString(s) }

// PRKey is the stable id of a pull request: github:owner/name#number.
func PRKey(owner, name string, number int) string {
	return fmt.Sprintf("github:%s/%s#%d", owner, name, number)
}

// ParsePRKey splits a PR key.
func ParsePRKey(k string) (owner, name string, number int, ok bool) {
	m := prKeyShape.FindStringSubmatch(k)
	if m == nil || !ValidRepoPart(m[1]) || !ValidRepoPart(m[2]) {
		return "", "", 0, false
	}
	fmt.Sscanf(m[3], "%d", &number)
	return m[1], m[2], number, true
}

// DedupeKey is the assignment dedupe key (unchanged from the browser orchestration):
// pr-assign:<pr_key>:<agent>:<task>. The Tether message idempotency key is this plus
// "#<n>" for the n-th reminder.
func DedupeKey(prKey, agent, task string) string {
	return "pr-assign:" + prKey + ":" + agent + ":" + task
}

// MessageKey is the Tether message idempotency key for the n-th send (0 = first).
func MessageKey(dedupe string, reminders int) string {
	if reminders == 0 {
		return dedupe
	}
	return fmt.Sprintf("%s#%d", dedupe, reminders)
}

// InlineAllowed says whether an inline comment may be posted: it must pin the
// current head, otherwise the line may point at changed code.
func InlineAllowed(commitID, head string) bool {
	return ValidHeadSHA(commitID) && strings.EqualFold(commitID, head)
}

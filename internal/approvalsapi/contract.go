// Package approvalsapi is the contract for the Approvals backend: the queue
// projection, batch resolve, destructive confirmation, defer, decision history,
// SSE, access and secret submission. It extends the existing inbox prefix
// (internal/approvals) additively; session, status, elevate and de-elevate stay
// with the real bridge. The stub in this package serves fixtures for GUI
// development (TACHYON_APPROVALS_STUBS=1); later slices replace its routes one
// by one. Design: tesseract key design_tachyon_approvals_backend_20261030.
package approvalsapi

import "slices"

// Prefix is the existing inbox prefix (approvals.Prefix); a test keeps them equal.
const Prefix = "/api/plugins/console/v1/approval-inbox"

// Risk is the item's server-set class.
const (
	RiskLow    = "low"
	RiskMedium = "medium"
	RiskHigh   = "high"
)

// Groups derive from kind + effects + action.
var Groups = []string{"command", "change", "destructive", "question", "form", "secret", "notice", "info"}

// RiskReasons explain why an item is not plain low risk.
var RiskReasons = []string{"effects_present", "secret_field", "interactive", "destructive", "command_exec"}

// EffectClasses normalize effects[].op for display and filtering.
var EffectClasses = []string{"create", "update", "restart", "delete", "archive", "run", "other"}

// Batch blockers, in the canonical order the server emits them.
const (
	BlockNotPending     = "not_pending"
	BlockKind           = "kind_not_batchable"
	BlockRisk           = "risk_not_low"
	BlockInteractive    = "interactive"
	BlockSecret         = "has_secret"
	BlockCommand        = "command_exec"
	BlockDestructive    = "destructive"
	BlockNoEffects      = "no_effects_declared"
	BlockIrreversible   = "irreversible_effect"
	BlockExpired        = "expired"
	BlockDeferred       = "deferred"
	BlockOperatorAccess = "needs_operator_access"
)

// BlockerOrder is the canonical order of batch_blockers.
var BlockerOrder = []string{BlockNotPending, BlockKind, BlockRisk, BlockInteractive, BlockSecret, BlockCommand,
	BlockDestructive, BlockNoEffects, BlockIrreversible, BlockExpired, BlockDeferred, BlockOperatorAccess}

// BatchOps is the built-in whitelist of effect ops a batch may carry. The app
// setting approvals.batch_ops may add ops; delete, archive and run can never be added.
var BatchOps = []string{"create", "update", "restart"}

// NeverBatchOps cannot be added to the whitelist by setting.
var NeverBatchOps = []string{"delete", "archive", "run"}

// Batch result statuses.
var BatchStatuses = []string{"resolved", "stale", "ineligible", "expired", "conflict", "error"}

// Run states joined from action runs / card receipts (state only for browser sessions).
var RunStates = []string{"none", "waiting", "approved", "starting", "running", "succeeded", "failed", "refused", "expired"}

// History / decision-log event kinds.
var EventKinds = []string{"presented", "deferred", "undeferred", "resolved", "batch_resolved", "expired", "withdrawn", "confirmation_failed", "access_denied"}

// SSE event types. Ids are a replayable sequence; a gap yields "resync".
var StreamEvents = []string{"inbox.changed", "item.created", "item.updated", "item.resolved", "item.expired", "item.deferred", "item.undeferred", "resync"}

// Error codes a client can branch on (HTTP status in brackets).
var ErrorCodes = []string{
	"stale_revision",           // 409
	"already_resolved",         // 409, carries the existing outcome
	"batch_id_reuse",           // 409
	"confirmation_required",    // 422, typed phrase missing or wrong
	"invalid_secret_ref",       // 422
	"operator_access_required", // 403
	"requester_cannot_approve", // 403
	"session_required",         // 401
	"secrets_unavailable",      // 503
	"reason_required",          // 422, deny needs a note when the item says so
	"rate_limited",             // 429
	"invalid",                  // 400
	"not_found",                // 404
}

// Limits.
const (
	MaxBatchItems     = 25
	MaxAffectedItems  = 200
	MaxAlternatives   = 3
	MaxEvidence       = 16
	MaxEffects        = 32
	DefaultDeferS     = 3600
	MinDeferS         = 60
	MaxDeferS         = 604800
	MaxSecretBytes    = 16 << 10
	MaxSecretFileSize = 64 << 10
	MaxLiveSecrets    = 20
	SecretRatePerMin  = 10
)

// Subject holds the facts eligibility depends on. The server builds it from the
// record and overlays; the UI builds it from the projection. Both must agree with
// fixtures/eligibility_table.json.
type Subject struct {
	Kind           string   `json:"kind"`
	Class          string   `json:"class"`
	Interactive    bool     `json:"interactive"`
	HasSecret      bool     `json:"has_secret"`
	CommandExec    bool     `json:"command_exec"`
	Destructive    bool     `json:"destructive"`
	EffectOps      []string `json:"effect_ops"`
	State          string   `json:"state"`
	Expired        bool     `json:"expired"`
	Deferred       bool     `json:"deferred"`
	OperatorAccess bool     `json:"operator_access"`
}

var pendingStates = []string{"submitted", "validated", "staged", "presented", "in_progress"}

// Eligibility is the server-side batch rule (design section 3): unknown means no.
// It returns the blockers in BlockerOrder; empty means batchable. extraOps are
// ops added by the approvals.batch_ops setting (never delete, archive or run).
func Eligibility(s Subject, extraOps ...string) []string {
	if !slices.Contains(pendingStates, s.State) {
		return []string{BlockNotPending}
	}
	ok := slices.Clone(BatchOps)
	for _, o := range extraOps {
		if !slices.Contains(NeverBatchOps, o) {
			ok = append(ok, o)
		}
	}
	var b []string
	add := func(cond bool, code string) {
		if cond {
			b = append(b, code)
		}
	}
	add(s.Kind != "approval" && s.Kind != "card.approval", BlockKind)
	add(s.Class != RiskLow, BlockRisk)
	add(s.Interactive, BlockInteractive)
	add(s.HasSecret, BlockSecret)
	add(s.CommandExec, BlockCommand)
	add(s.Destructive, BlockDestructive)
	add(len(s.EffectOps) == 0, BlockNoEffects)
	bad := false
	for _, o := range s.EffectOps {
		bad = bad || !slices.Contains(ok, o)
	}
	add(bad, BlockIrreversible)
	add(s.Expired, BlockExpired)
	add(s.Deferred, BlockDeferred)
	add(!s.OperatorAccess, BlockOperatorAccess)
	return b
}

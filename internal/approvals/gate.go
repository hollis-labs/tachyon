package approvals

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"

	"github.com/hollis-labs/libs/mesh/core/hitl"
)

// Reasons an item cannot be approved in the browser (approvable_reason). deferred and decision_pending
// are reserved by the contract and not produced: a deferred item can still be decided, and decisions
// are not held on the server (undo window, option A).
const (
	ReasonExpired         = "expired"
	ReasonAlreadyResolved = "already_resolved"
	ReasonUnsupportedKind = "unsupported_class"
)

// Gate is the answer of CanResolve: whether the browser may decide the item at all, why not, and the
// confirmation phrase an approval must carry.
type Gate struct {
	Approvable bool
	Reason     string
	// Phrase is set for stored-high and cerberus items: approving needs response.confirmation equal to it.
	Phrase string
}

// CanResolve is the single gate for deciding an item from the browser. The projection (approvable,
// approvable_reason, confirm) and the resolve handler both call it, so the UI and the server cannot
// drift. What assurance the session needs is separate (assurance.Requirements).
//
// Stored-high and cerberus-source items used to be a provider handoff only. By decision of the
// operator (A3a, CW-20261030-0129; the hardening revisit is CW-20261030-0082) they are now decided
// here, behind operator assurance and a server-derived typed phrase. Temporary by design.
func CanResolve(rec hitl.Record, now time.Time) Gate {
	if rec.State.IsTerminal() {
		return Gate{Reason: ReasonAlreadyResolved}
	}
	if rec.ExpiresAt != nil && !rec.ExpiresAt.After(now) {
		return Gate{Reason: ReasonExpired}
	}
	var req hitl.EnqueueRequest
	_ = json.Unmarshal(rec.Request, &req)
	switch class := extraString(req, "class"); {
	case class == "high" || req.Source.ApplicationID == "cerberus":
		return Gate{Approvable: true, Phrase: ConfirmPhrase(rec.ItemID, req)}
	case class == "low" || class == "medium":
		return Gate{Approvable: true}
	}
	return Gate{Reason: ReasonUnsupportedKind}
}

// ConfirmPhrase derives the typed phrase from the item and what it would run: the op_digest when the
// request has one (registered action runs, so the phrase and the Start-time digest re-verification
// cover the same operation), otherwise a hash of the stored request. The asker cannot choose it.
func ConfirmPhrase(itemID string, req hitl.EnqueueRequest) string {
	basis := extraString(req, "op_digest")
	if basis == "" {
		raw, _ := json.Marshal(req)
		sum := sha256.Sum256(raw)
		basis = hex.EncodeToString(sum[:])
	}
	sum := sha256.Sum256([]byte("approval-confirm|" + itemID + "|" + basis))
	return "APPROVE " + strings.ToUpper(hex.EncodeToString(sum[:])[:8])
}

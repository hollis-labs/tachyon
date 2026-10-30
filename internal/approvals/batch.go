package approvals

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"slices"
	"sync"
	"time"

	"github.com/hollis-labs/libs/mesh/core/hitl"
	"github.com/hollis-labs/tachyon/internal/approvals/assurance"
	"github.com/hollis-labs/tachyon/internal/approvalsapi"
	"github.com/hollis-labs/tachyon/internal/cards"
)

const batchKeep = 7 * 24 * time.Hour

var batchIDPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,64}$`)

// batchMu makes a batch and its stored answer one step, so a retry that arrives while the first try is still
// running waits and then replays instead of deciding the items twice. Batches are small and serial anyway.
var batchMu sync.Mutex

type batchError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type batchResult struct {
	ItemID   string      `json:"item_id"`
	Status   string      `json:"status"`
	Decision string      `json:"decision,omitempty"`
	Revision int64       `json:"revision,omitempty"`
	Blockers []string    `json:"blockers,omitempty"`
	Error    *batchError `json:"error,omitempty"`
}

type batchCommand struct {
	BatchID  string `json:"batch_id"`
	Decision string `json:"decision"`
	Note     string `json:"note,omitempty"`
	Items    []struct {
		ItemID                      string `json:"item_id"`
		ExpectedRevision            int64  `json:"expected_revision"`
		PresentedProjectionRevision int64  `json:"presented_projection_revision"`
	} `json:"items"`
}

// batchResolve is POST /batch/resolve: up to 25 low-risk approvals decided one at a time, in request order,
// through the same Respond call as a single resolve. It is not a transaction: one item failing never stops the
// next. Eligibility is recomputed from a fresh read of each record (the client's projection is not trusted).
func (i *Inbox) batchResolve(w http.ResponseWriter, r *http.Request, participant hitl.Participant, session sessionStatus) {
	if !assurance.Satisfies(assurance.ParseLevel(participant.Assurance), assurance.ActionBatch) {
		fail(w, 403, "assurance_insufficient", "Enter your operator key to decide a batch. "+ElevationCopy(session.ElevationTTL, session.ElevationExpiresAt))
		return
	}
	raw, err := commandBody(w, r)
	if err != nil {
		writeHITLError(w, err)
		return
	}
	var cmd batchCommand
	if err = strictJSON(raw, &cmd); err != nil {
		writeHITLError(w, err)
		return
	}
	if !batchIDPattern.MatchString(cmd.BatchID) || (cmd.Decision != "approved" && cmd.Decision != "denied") || len(cmd.Note) > 2000 ||
		len(cmd.Items) < 1 || len(cmd.Items) > approvalsapi.MaxBatchItems {
		fail(w, 400, "invalid", "Send batch_id, decision (approved or denied) and 1 to 25 items.")
		return
	}
	seen := map[string]bool{}
	for _, it := range cmd.Items {
		if it.ItemID == "" || it.ExpectedRevision < 1 || it.PresentedProjectionRevision < 1 || seen[it.ItemID] {
			fail(w, 400, "invalid", "Each item needs item_id, expected_revision and presented_projection_revision, and appears once.")
			return
		}
		seen[it.ItemID] = true
	}
	canon, _ := json.Marshal(cmd)
	sum := sha256.Sum256(canon)
	digest := hex.EncodeToString(sum[:])

	batchMu.Lock()
	defer batchMu.Unlock()
	var prevDigest, prevResponse string
	switch err := i.Store.DB.Reader.QueryRowContext(r.Context(), `SELECT request_digest,response FROM approval_batches WHERE batch_id=?`, cmd.BatchID).Scan(&prevDigest, &prevResponse); {
	case err == nil:
		if prevDigest != digest {
			fail(w, 409, "batch_id_reuse", "This batch_id was already used with a different request.")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Tachyon-Batch-Replay", "1")
		_, _ = w.Write([]byte(prevResponse))
		return
	case !errors.Is(err, sql.ErrNoRows):
		fail(w, 500, "internal", "The batch could not be checked. Nothing was decided.")
		return
	}

	counts := map[string]int{}
	results := make([]batchResult, 0, len(cmd.Items))
	for _, in := range cmd.Items {
		res := i.batchOne(r, participant, cmd, in.ItemID, in.ExpectedRevision, in.PresentedProjectionRevision)
		counts[res.Status]++
		results = append(results, res)
	}
	body, _ := json.Marshal(map[string]any{"batch_id": cmd.BatchID, "results": results, "counts": counts, "completed_at": time.Now().UTC().Format(time.RFC3339)})
	// The decisions are made. A failure to store the answer only costs the replay, never the result.
	_ = i.Store.DB.Writer.Submit(r.Context(), "approval_batch_store", func(ctx context.Context, tx *sql.Tx) error {
		now := time.Now()
		if _, err := tx.ExecContext(ctx, `DELETE FROM approval_batches WHERE created_ms<?`, now.Add(-batchKeep).UnixMilli()); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO approval_batches(batch_id,request_digest,response,created_ms) VALUES(?,?,?,?)`, cmd.BatchID, digest, string(body), now.UnixMilli())
		return err
	})
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(body)
}

func (i *Inbox) batchOne(r *http.Request, participant hitl.Participant, cmd batchCommand, id string, expected, presented int64) batchResult {
	res := batchResult{ItemID: id}
	itemErr := func(code, msg string) batchResult {
		res.Status, res.Error = "error", &batchError{Code: code, Message: msg}
		return res
	}
	item, err := i.view(r, id, true)
	if err != nil {
		if errors.Is(err, hitl.ErrNotFound) {
			return itemErr("not_found", "No such item.")
		}
		return itemErr("internal", "The item could not be read. It stays in the queue.")
	}
	if item.State.IsTerminal() {
		res.Status, res.Revision = "conflict", item.Revision
		if item.TerminalOutcome != nil && item.TerminalOutcome.OutcomeState() == hitl.StateExpired {
			res.Status = "expired"
		}
		if o, ok := item.TerminalOutcome.(hitl.Resolved); ok {
			res.Decision = o.Resolution.Response.Decision
		}
		return res
	}
	var request hitl.EnqueueRequest
	_ = json.Unmarshal(item.RequestSnapshot, &request)
	blockers := slices.Clone(item.Projection.BatchBlockers)
	if request.Source.ApplicationID == "cerberus" && !slices.Contains(blockers, approvalsapi.BlockRisk) {
		blockers = append(blockers, approvalsapi.BlockRisk) // provider-console decisions never batch
	}
	if len(blockers) > 0 {
		res.Status, res.Blockers = "ineligible", blockers
		if len(blockers) == 1 && blockers[0] == approvalsapi.BlockExpired {
			res.Status = "expired"
		}
		return res
	}
	if expected != item.Revision || presented != item.Revision {
		res.Status, res.Revision = "stale", item.Revision
		res.Error = &batchError{Code: "stale_revision", Message: "The item changed. Review it again."}
		return res
	}
	if cmd.Decision == "denied" && item.Projection.ReasonReq && cmd.Note == "" {
		return itemErr("reason_required", "This item needs a reason to deny.")
	}
	response := hitl.Response{Kind: request.Kind, Decision: cmd.Decision, Extra: map[string]json.RawMessage{}}
	if cmd.Note != "" {
		response.Extra["note"], _ = json.Marshal(cmd.Note)
	}
	if cards.IsCard(request.Kind) {
		if err = CardRegistry.ValidateAnswers(request, response); err != nil {
			return itemErr("invalid", "This card needs an answer and cannot be decided in a batch.")
		}
	}
	outcome, err := i.Service.Respond(r.Context(), hitl.RespondCommand{ItemID: id, ExpectedRevision: &expected, Response: response, Participant: participant})
	var stale *hitl.StaleRevisionError
	var terminal *hitl.TerminalConflictError
	switch {
	case err == nil:
		res.Status, res.Decision, res.Revision = "resolved", cmd.Decision, outcome.OutcomeRevision()
	case errors.As(err, &stale):
		res.Status, res.Revision = "stale", stale.Actual
		res.Error = &batchError{Code: "stale_revision", Message: "The item changed. Review it again."}
	case errors.As(err, &terminal):
		res.Status = "conflict"
		if terminal.Outcome != nil {
			res.Revision = terminal.Outcome.OutcomeRevision()
			if terminal.Outcome.OutcomeState() == hitl.StateExpired {
				res.Status = "expired"
			}
			if o, ok := terminal.Outcome.(hitl.Resolved); ok {
				res.Decision = o.Resolution.Response.Decision
			}
		}
	default:
		return itemErr("internal", "The decision failed. The item stays in the queue.")
	}
	return res
}

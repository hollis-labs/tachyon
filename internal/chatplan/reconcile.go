package chatplan

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/hollis-labs/tachyon/internal/chatapi"
)

var ErrContinuationUnavailable = errors.New("continuation evidence unavailable")
var ErrContinuationUnverified = errors.New("continuation evidence unverified")

// ReadReconciler repairs UI lineage for restarts performed outside Tachyon.
// Verification completes for the whole bounded chain before any local mutation.
// It never supplies native admission or changes the public continuation gate.
type ReadReconciler struct {
	Service  *Service
	Verifier *TetherContinuationVerifier
	// Candidates lists running replacements from canonical Tether enumeration.
	Candidates func(context.Context) ([]string, error)
}

func terminal(state string) bool {
	return state == "completed" || state == "failed" || state == "killed"
}

func nativeEvidenceMatches(old, next tetherContinuationSession, agent string) bool {
	a := next.NativeAdmission
	return a != nil && a.Kind == "codex_same_worker_v1" && a.SourceSessionID == old.ID && a.ReplacementSessionID == next.ID &&
		a.LogicalAgentID == agent && a.SourceGeneration != "" && a.ReplacementGeneration != "" && a.NativeThreadID != "" && a.AdmittedAt != "" &&
		a.WorkerID != "" && old.WorkerID == a.WorkerID && next.WorkerID == a.WorkerID
}

func receiptTimeValid(value string) bool {
	at, err := time.Parse(time.RFC3339Nano, value)
	return err == nil && !at.IsZero()
}

func teamEvidenceMatches(old, next tetherContinuationSession, agent string) bool {
	a := next.ReplacementLineage
	return a != nil && a.Kind == "retained_team_v1" && a.SourceSessionID == old.ID && a.ReplacementSessionID == next.ID &&
		a.LogicalAgentID == agent && a.ActorURI != "" && a.SourceActorURI == a.ActorURI && a.ReplacementActorURI == a.ActorURI && receiptTimeValid(a.CommittedAt) && a.IntentKey != "" && a.BindingID != "" && a.BindingGeneration > 0 &&
		old.WorkerID == "" && next.WorkerID == ""
}

// workerEvidenceMatches accepts only the distinct ordinary-launch receipt;
// generic running/resume rows and state frames never establish this evidence.
func workerEvidenceMatches(old, next tetherContinuationSession, agent string) bool {
	a := next.WorkerLineage
	return a != nil && a.Kind == "worker_lineage_v1" && a.SourceSessionID == old.ID && a.ReplacementSessionID == next.ID && a.LogicalAgentID == agent &&
		a.SourceWorkerID == old.WorkerID && a.WorkerID != "" && a.WorkerID == next.WorkerID && a.CommandID == "launch:"+next.ID && receiptTimeValid(a.AcknowledgedAt)
}

// Ensure reconstructs missing ancestry for a running replacement. Complete
// chains and explicit clear fences remain readable without remote repair. Their
// local markers are cache bookkeeping, not proof for any native operation.
func (r *ReadReconciler) Ensure(ctx context.Context, id string) error {
	if r == nil || r.Service == nil || r.Service.Store == nil || r.Verifier == nil {
		return ErrContinuationUnavailable
	}
	found, err := r.Service.Store.HasReconciliationBoundary(ctx, id)
	if err != nil {
		return ErrContinuationUnavailable
	}
	if found {
		return nil
	}
	next, err := r.Verifier.session(ctx, id)
	if err != nil {
		return ErrContinuationUnavailable
	}
	if next.ID != id || next.ID == "" {
		return ErrContinuationUnverified
	}
	if next.Intent != "resume" {
		return nil
	}
	// Existing historical local threads still render their already-recorded
	// history; this read path does not invent admission for a terminal tip.
	if next.State != "running" {
		if terminal(next.State) || next.State == "orphaned" {
			return nil
		}
		return ErrContinuationUnverified
	}
	agent := next.LogicalAgentID
	if agent == "" {
		return ErrContinuationUnverified
	}
	seen := map[string]bool{id: true}
	var reverse []ContinuationLink
	historical, retiredByTeam := false, false
	for {
		if next.Intent != "resume" || next.ParentSessionID == "" || seen[next.ParentSessionID] || len(reverse) >= MaxLineageHops {
			return ErrContinuationUnverified
		}
		source := next.ParentSessionID
		old, err := r.Verifier.session(ctx, source)
		if err != nil {
			return ErrContinuationUnavailable
		}
		if old.ID != source || old.LogicalAgentID != agent || next.LogicalAgentID != agent {
			return ErrContinuationUnverified
		}
		team := teamEvidenceMatches(old, next, agent)
		if !terminal(old.State) && !(old.State == "orphaned" && team) {
			return ErrContinuationUnverified
		}
		if historical && !terminal(next.State) && !(next.State == "orphaned" && retiredByTeam) {
			return ErrContinuationUnverified
		}
		switch {
		case team:
			// The producer binds actor/agent to the committed receipt; this is UI
			// execution lineage, never native context or custody authority.
		case old.WorkerID == "" && next.WorkerID == "":
			if old.State == "orphaned" {
				return ErrContinuationUnverified
			}
		case nativeEvidenceMatches(old, next, agent):
		case workerEvidenceMatches(old, next, agent):
		default:
			return ErrContinuationUnverified
		}
		reverse = append(reverse, ContinuationLink{Source: source, Replacement: next.ID, Agent: agent})
		seen[source] = true
		known, err := r.Service.Store.HasReconciliationBoundary(ctx, source)
		if err != nil {
			return ErrContinuationUnavailable
		}
		if known || old.Intent != "resume" {
			break
		}
		next, historical, retiredByTeam = old, true, team
	}
	links := make([]ContinuationLink, len(reverse))
	for i := range reverse {
		links[len(reverse)-1-i] = reverse[i]
	}
	results, err := r.Service.Store.AssociateChain(ctx, links)
	if err != nil {
		if errors.Is(err, ErrAgentMismatch) || errors.Is(err, ErrAssociationConflict) {
			return ErrContinuationUnverified
		}
		return ErrContinuationUnavailable
	}
	for _, result := range results {
		r.Service.publishCarry(ctx, result.Carried)
	}
	return nil
}

// Wrap runs before ordinary plan, thread and message GETs. It takes no caller
// lineage assertions: IDs only select server-owned Tether evidence.
func (r *ReadReconciler) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodGet {
			next.ServeHTTP(w, req)
			return
		}
		ctx, cancel := context.WithTimeout(req.Context(), 30*time.Second)
		defer cancel()
		path := strings.TrimPrefix(req.URL.Path, chatapi.Prefix)
		var ids []string
		var err error
		if path == "/threads" && r.Candidates != nil {
			ids, err = r.Candidates(ctx)
		} else if strings.HasPrefix(path, "/threads/") {
			ref, _, _ := strings.Cut(strings.TrimPrefix(path, "/threads/"), "/")
			if parsed, e := chatapi.ParseRef(ref); e == nil && parsed.Source == chatapi.SourceTether {
				ids = []string{parsed.ID}
			}
		}
		if err == nil {
			for _, id := range ids {
				if err = r.Ensure(ctx, id); err != nil {
					break
				}
			}
		}
		if err != nil {
			if errors.Is(err, ErrContinuationUnverified) {
				fail(w, 409, "continuation_unverified", "Tether did not verify this chat continuation.")
			} else {
				fail(w, 503, "continuation_unavailable", "Chat continuity could not be verified. Retry the read.")
			}
			return
		}
		next.ServeHTTP(w, req)
	})
}

// RunningContinuations discovers replacements without depending on a proxy call,
// output event or a possibly stale collector snapshot. A truncated or looping
// page sequence is an error, never a silently complete result.
func (v *TetherContinuationVerifier) RunningContinuations(ctx context.Context) ([]string, error) {
	if v == nil || v.Client == nil || v.Base == "" {
		return nil, ErrContinuationUnavailable
	}
	var ids []string
	cursor := ""
	cursors, seen := map[string]bool{}, map[string]bool{}
	for page := 0; page < 10; page++ {
		query := url.Values{"state": {"running"}, "limit": {"100"}}
		if cursor != "" {
			query.Set("cursor", cursor)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(v.Base, "/")+"/sessions?"+query.Encode(), nil)
		if err != nil {
			return nil, ErrContinuationUnavailable
		}
		resp, err := v.Client.Do(req)
		if err != nil {
			return nil, ErrContinuationUnavailable
		}
		var out struct {
			Sessions   []tetherContinuationSession `json:"sessions"`
			NextCursor string                      `json:"next_cursor"`
		}
		err = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK || err != nil || out.Sessions == nil {
			return nil, ErrContinuationUnavailable
		}
		for _, row := range out.Sessions {
			if row.ID == "" {
				return nil, ErrContinuationUnverified
			}
			if row.State == "running" && row.Intent == "resume" && !seen[row.ID] {
				ids = append(ids, row.ID)
				seen[row.ID] = true
			}
		}
		if out.NextCursor == "" {
			return ids, nil
		}
		if cursors[out.NextCursor] {
			return nil, ErrContinuationUnavailable
		}
		cursor = out.NextCursor
		cursors[cursor] = true
	}
	return nil, ErrContinuationUnavailable
}

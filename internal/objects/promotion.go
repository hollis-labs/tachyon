package objects

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

type PromotionRequest struct {
	SourceID         string          `json:"source_id"`
	ExpectedRevision int64           `json:"expected_revision"`
	TargetID         string          `json:"target_id"` // caller-retained canonical UUID for retries
	TargetKind       Kind            `json:"target_kind"`
	Overrides        json.RawMessage `json:"overrides,omitempty"`     // merge patch of copied common fields
	TriageSource     bool            `json:"triage_source,omitempty"` // portfolio inbox cutover; atomic with target creation
}
type PromotionReceipt struct {
	SourceID       string    `json:"source_id"`
	TargetID       string    `json:"target_id"`
	SourceRevision int64     `json:"source_revision"` // exact source used, before promotion
	TargetRevision int64     `json:"target_revision"` // initial target, before later edits
	Actor          string    `json:"actor"`
	CreatedAt      time.Time `json:"created_at"`
}
type PromotionResult struct {
	Source  Record           `json:"source"` // current records on a replay; receipt pins original revisions
	Target  Record           `json:"target"`
	Receipt PromotionReceipt `json:"receipt"`
}

func promotionReceipt(ctx context.Context, q queryer, sourceID string) (PromotionReceipt, string, error) {
	var r PromotionReceipt
	var request, at string
	err := q.QueryRowContext(ctx, `SELECT source_id,target_id,source_revision,target_revision,actor,created_at,request_json FROM planner_promotions WHERE source_id=?`, sourceID).Scan(&r.SourceID, &r.TargetID, &r.SourceRevision, &r.TargetRevision, &r.Actor, &at, &request)
	if errors.Is(err, sql.ErrNoRows) {
		return r, "", ErrNotFound
	}
	if err != nil {
		return r, "", err
	}
	r.CreatedAt, err = time.Parse(stamp, at)
	return r, request, err
}
func (s *Service) GetPromotion(ctx context.Context, sourceID string) (PromotionReceipt, error) {
	r, _, err := promotionReceipt(ctx, s.store.Reader, sourceID)
	return r, err
}

// Promote atomically creates a distinct owned record, preserves source content,
// and records immutable provenance. Inbox -> portfolio kinds/plan/task;
// idea -> plan/task. TriageSource atomically marks an inbox triaged when requested.
// Each source has one canonical promotion. Same request replays without writes,
// even after subsequent edits/deletion; another request for that source conflicts.
// This does not dispatch work, copy a provider identity or change source status.
func (s *Service) Promote(ctx context.Context, req PromotionRequest, actor string) (PromotionResult, error) {
	var out PromotionResult
	id, err := uuid.Parse(req.TargetID)
	if err != nil || id.String() != req.TargetID || strings.TrimSpace(actor) == "" || req.ExpectedRevision < 1 || req.SourceID == "" {
		return out, fmt.Errorf("%w: source, revision, canonical target UUID and actor required", ErrInvalid)
	}
	if req.TargetKind != IdeaKind && req.TargetKind != PlanKind && req.TargetKind != TaskKind && req.TargetKind != WorkstreamKind && req.TargetKind != RoadmapItemKind && req.TargetKind != StrategicPriorityKind && req.TargetKind != DecisionKind && req.TargetKind != RiskKind {
		return out, ErrInvalid
	}
	req.Overrides, err = canonical(req.Overrides)
	if err != nil {
		return out, err
	}
	request, err := json.Marshal(req)
	if err != nil {
		return out, err
	}
	err = s.store.Writer.Submit(ctx, "planner_promote", func(ctx context.Context, tx *sql.Tx) error {
		receipt, existing, err := promotionReceipt(ctx, tx, req.SourceID)
		if err == nil {
			if existing != string(request) {
				return ErrConflict
			}
			source, err := get(ctx, tx, receipt.SourceID)
			if err != nil {
				return err
			}
			target, err := get(ctx, tx, receipt.TargetID)
			if err != nil {
				return err
			}
			out = PromotionResult{Source: source, Target: target, Receipt: receipt}
			return nil
		}
		if !errors.Is(err, ErrNotFound) {
			return err
		}
		source, err := get(ctx, tx, req.SourceID)
		if err != nil {
			return err
		}
		if source.Revision != req.ExpectedRevision {
			return ErrConflict
		}
		if source.Ownership != "tachyon" || source.Deleted() || (source.Kind != InboxItemKind && source.Kind != IdeaKind) || (source.Kind == IdeaKind && (req.TargetKind != PlanKind && req.TargetKind != TaskKind || req.TriageSource)) {
			return fmt.Errorf("%w: promotion requires an active owned inbox item or idea and an allowed destination", ErrInvalid)
		}
		if _, err := get(ctx, tx, req.TargetID); err == nil {
			return ErrConflict
		} else if !errors.Is(err, ErrNotFound) {
			return err
		}
		// Only compatible common fields carry over. The receipt preserves access to
		// capture-specific details (origin, hypothesis, etc.) in the original revision.
		fields, err := jsonObject(source.Definition)
		if err != nil {
			return err
		}
		copied := map[string]json.RawMessage{}
		for _, key := range []string{"name", "description", "tags", "properties", "priority", "project_id", "workstream_id"} {
			if value, ok := fields[key]; ok {
				copied[key] = value
			}
		}
		copied["status"] = json.RawMessage(`"draft"`)
		if req.TargetKind == TaskKind {
			copied["status"] = json.RawMessage(`"todo"`)
		}
		if req.TargetKind == DecisionKind {
			copied["status"] = json.RawMessage(`"proposed"`)
		}
		if req.TargetKind == RiskKind {
			copied["status"] = json.RawMessage(`"open"`)
		}
		if req.TargetKind == DecisionKind || req.TargetKind == WorkstreamKind {
			delete(copied, "priority")
		}
		if req.TargetKind == WorkstreamKind {
			delete(copied, "workstream_id")
		}
		// Keep arbitrary property numbers as raw JSON: decoding through any
		// would round integers above float64's exact range.
		seed, err := json.Marshal(copied)
		if err != nil {
			return err
		}
		definition, err := merge(seed, req.Overrides, true)
		if err != nil {
			return err
		}
		target := Record{ID: req.TargetID, Kind: req.TargetKind, Ownership: "tachyon", Local: definition, Metadata: source.Metadata}
		if err = persist(ctx, tx, &target, "promote_create", actor); err != nil {
			return err
		}
		receipt = PromotionReceipt{SourceID: source.ID, TargetID: target.ID, SourceRevision: source.Revision, TargetRevision: target.Revision, Actor: actor, CreatedAt: now()}
		_, err = tx.ExecContext(ctx, `INSERT INTO planner_promotions(source_id,target_id,source_revision,target_revision,request_json,actor,created_at) VALUES(?,?,?,?,?,?,?)`, receipt.SourceID, receipt.TargetID, receipt.SourceRevision, receipt.TargetRevision, string(request), actor, timestamp(receipt.CreatedAt))
		if err != nil {
			return err
		}
		if req.TriageSource {
			source.Local, err = merge(source.Local, json.RawMessage(`{"status":"triaged"}`), true)
			if err != nil {
				return err
			}
		}
		if err = persist(ctx, tx, &source, "promote", actor); err != nil {
			return err
		}
		if err = projectPromotionLinks(ctx, tx, target.ID); err != nil {
			return err
		}
		out = PromotionResult{Source: source, Target: target, Receipt: receipt}
		return nil
	})
	if err != nil {
		return PromotionResult{}, err
	}
	return out, nil
}

// Receipt-owned edges are rebuilt alongside definition-owned edges so patches,
// observation refreshes, ownership changes and tombstones cannot erase provenance.
func projectPromotionLinks(ctx context.Context, tx *sql.Tx, id string) error {
	_, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO object_links(from_id,relation,slot,to_id)
 SELECT source_id,'promoted_to','',target_id FROM planner_promotions WHERE source_id=?
 UNION ALL SELECT target_id,'promoted_from','',source_id FROM planner_promotions WHERE target_id=?`, id, id)
	return err
}

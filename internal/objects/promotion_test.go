package objects

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/hollis-labs/tachyon/internal/localstore"
)

func capture(t *testing.T, s *Service) Record {
	t.Helper()
	return create(t, s, InboxItem{CaptureFields: CaptureFields{Common: Common{Name: "Capture", Description: "Original detail", Tags: []string{"durable"}, Properties: map[string]any{"custom": "retained"}}, Status: "new", Priority: 2}, Origin: "operator"})
}
func promotion(source Record, kind Kind) PromotionRequest {
	return PromotionRequest{SourceID: source.ID, ExpectedRevision: source.Revision, TargetID: uuid.NewString(), TargetKind: kind}
}

func TestPromotionPreservesHistoryAndSurvivesRetry(t *testing.T) {
	s, _, path := open(t)
	source := capture(t, s)
	source, err := s.Patch(ctx, source.ID, source.Revision, Patch{Definition: json.RawMessage(`{"properties":{"large_integer":9007199254740993}}`)}, "operator")
	if err != nil {
		t.Fatal(err)
	}
	req := promotion(source, IdeaKind)
	req.Overrides = json.RawMessage(`{"name":"Explore ownership","hypothesis":"Local data stays available"}`)
	result, err := s.Promote(ctx, req, "operator")
	if err != nil {
		t.Fatal(err)
	}
	if result.Source.Revision != source.Revision+1 || string(result.Source.Definition) != string(source.Definition) || result.Target.Kind != IdeaKind || result.Target.Source != nil || result.Target.Ownership != "tachyon" {
		t.Fatalf("unexpected promotion %+v", result)
	}
	var idea Idea
	if err = json.Unmarshal(result.Target.Definition, &idea); err != nil || idea.Description != "Original detail" || idea.Priority != 2 || idea.Status != "draft" || idea.Properties["custom"] != "retained" || string(result.Target.Metadata) != string(source.Metadata) {
		t.Fatal("copy failed", err, idea)
	}
	fields, err := jsonObject(result.Target.Definition)
	if err != nil {
		t.Fatal(err)
	}
	properties, err := jsonObject(fields["properties"])
	if err != nil || string(properties["large_integer"]) != "9007199254740993" {
		t.Fatal("property number changed", err)
	}
	history, err := s.History(ctx, source.ID, 0, 100)
	if err != nil || len(history) != 3 || history[2].Operation != "promote" || history[1].Record.Revision != result.Receipt.SourceRevision {
		t.Fatal("source history", err)
	}
	checkLinks := func(s *Service, from, to, relation string) {
		t.Helper()
		links, err := s.Links(ctx, from, false)
		if err != nil {
			t.Fatal(err)
		}
		for _, link := range links {
			if link.ToID == to && link.Relation == relation {
				return
			}
		}
		t.Fatalf("missing %s: %+v", relation, links)
	}
	checkLinks(s, source.ID, result.Target.ID, "promoted_to")
	checkLinks(s, result.Target.ID, source.ID, "promoted_from")
	target, err := s.Patch(ctx, result.Target.ID, result.Target.Revision, Patch{Definition: json.RawMessage(`{"description":"Later edit"}`)}, "editor")
	if err != nil {
		t.Fatal(err)
	}
	target, err = s.SetDeleted(ctx, target.ID, target.Revision, true, "editor")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Patch(ctx, result.Source.ID, result.Source.Revision, Patch{Definition: json.RawMessage(`{"status":"archived"}`)}, "editor"); err != nil {
		t.Fatal(err)
	}
	checkLinks(s, source.ID, target.ID, "promoted_to")
	checkLinks(s, target.ID, source.ID, "promoted_from")
	reopened, err := localstore.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	recovered := New(reopened)
	// Different whitespace/key order is the same canonical request.
	req.Overrides = json.RawMessage(`{ "hypothesis": "Local data stays available", "name": "Explore ownership" }`)
	replay, err := recovered.Promote(ctx, req, "retry-worker")
	if err != nil || replay.Target.Revision != target.Revision || !replay.Target.Deleted() || replay.Receipt != result.Receipt {
		t.Fatal("retry mutated target", err)
	}
	receipt, err := recovered.GetPromotion(ctx, source.ID)
	if err != nil || receipt != result.Receipt {
		t.Fatal("receipt lost", err)
	}
	rows, err := recovered.ListPlanner(ctx, PlannerFilter{Promoted: new(true)})
	if err != nil || len(rows) != 1 || rows[0].ID != source.ID {
		t.Fatal("promotion query", err)
	}
	rows, err = recovered.ListPlanner(ctx, PlannerFilter{Kind: InboxItemKind, Promoted: new(false)})
	if err != nil || len(rows) != 0 {
		t.Fatal("promoted capture returned as pending", err)
	}
	for _, change := range []func(*PromotionRequest){func(r *PromotionRequest) { r.TargetID = uuid.NewString() }, func(r *PromotionRequest) { r.ExpectedRevision++ }, func(r *PromotionRequest) { r.Overrides = json.RawMessage(`{"name":"Different"}`) }} {
		changed := req
		change(&changed)
		if _, err = recovered.Promote(ctx, changed, "test"); !errors.Is(err, ErrConflict) {
			t.Fatal("conflicting replay accepted", err)
		}
	}
}

func TestPromotionAllowedPathsAndValidation(t *testing.T) {
	s, _, _ := open(t)
	for _, kind := range []Kind{IdeaKind, PlanKind, TaskKind} {
		original := capture(t, s)
		result, err := s.Promote(ctx, promotion(original, kind), "test")
		if err != nil {
			t.Fatal(err)
		}
		if kind == IdeaKind {
			for _, destination := range []Kind{PlanKind, TaskKind} {
				idea := create(t, s, Idea{CaptureFields: CaptureFields{Common: Common{Name: "Idea"}, Status: "accepted"}})
				if _, err = s.Promote(ctx, promotion(idea, destination), "test"); err != nil {
					t.Fatal(err)
				}
			}
			if _, err = s.Promote(ctx, promotion(result.Target, IdeaKind), "test"); !errors.Is(err, ErrInvalid) {
				t.Fatal("idea -> idea", err)
			}
		}
	}
	source := capture(t, s)
	req := promotion(source, TaskKind)
	req.ExpectedRevision++
	if _, err := s.Promote(ctx, req, "test"); !errors.Is(err, ErrConflict) {
		t.Fatal("stale revision", err)
	}
	req = promotion(source, TaskKind)
	req.Overrides = json.RawMessage(`{"plan_id":"missing"}`)
	if _, err := s.Promote(ctx, req, "test"); !errors.Is(err, ErrInvalid) {
		t.Fatal("invalid reference", err)
	}
	if _, err := s.Get(ctx, req.TargetID); !errors.Is(err, ErrNotFound) {
		t.Fatal("failed target persisted", err)
	}
	if _, err := s.GetPromotion(ctx, source.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("failed receipt persisted", err)
	}
	existing := create(t, s, Plan{Common: Common{Name: "Existing"}, Status: "draft"})
	req = promotion(source, PlanKind)
	req.TargetID = existing.ID
	if _, err := s.Promote(ctx, req, "test"); !errors.Is(err, ErrConflict) {
		t.Fatal("adopted existing target", err)
	}
	deleted, err := s.SetDeleted(ctx, source.ID, source.Revision, true, "test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Promote(ctx, promotion(deleted, PlanKind), "test"); !errors.Is(err, ErrInvalid) {
		t.Fatal("promoted deleted capture", err)
	}
	obs := Observation{Key: SourceKey{SourceScope: SourceScope{Provider: "test", Instance: "host", Collection: "inbox", Kind: InboxItemKind}, ExternalID: "remote"}, ObservedAt: now(), Definition: body(t, InboxItem{CaptureFields: CaptureFields{Common: Common{Name: "Remote"}, Status: "new"}})}
	remote := observe(t, s, obs)
	if _, err = s.Promote(ctx, promotion(remote, TaskKind), "test"); !errors.Is(err, ErrInvalid) {
		t.Fatal("promoted source-owned", err)
	}
}

func TestPromotionConcurrentRequests(t *testing.T) {
	for _, same := range []bool{false, true} {
		t.Run(fmt.Sprint(same), func(t *testing.T) {
			s, _, _ := open(t)
			source := capture(t, s)
			a := promotion(source, PlanKind)
			b := a
			if !same {
				b.TargetID = uuid.NewString()
			}
			var wg sync.WaitGroup
			results := make(chan error, 2)
			for _, req := range []PromotionRequest{a, b} {
				wg.Add(1)
				go func() { defer wg.Done(); _, err := s.Promote(ctx, req, "test"); results <- err }()
			}
			wg.Wait()
			close(results)
			success, conflict := 0, 0
			for err := range results {
				if err == nil {
					success++
				} else if errors.Is(err, ErrConflict) {
					conflict++
				} else {
					t.Fatal(err)
				}
			}
			if same && success != 2 || !same && (success != 1 || conflict != 1) {
				t.Fatalf("outcomes %d %d", success, conflict)
			}
			rows, err := s.List(ctx, Filter{Kind: PlanKind})
			if err != nil || len(rows) != 1 {
				t.Fatal("duplicate target", err)
			}
			history, err := s.History(ctx, source.ID, 0, 100)
			if err != nil || len(history) != 2 {
				t.Fatal("duplicate promotion history", err)
			}
		})
	}
}

func TestPromotionRollsBackAfterTargetAndReceiptWrites(t *testing.T) {
	s, db, _ := open(t)
	source := capture(t, s)
	req := promotion(source, TaskKind)
	// Force failure late in the transaction, after target, revision and receipt.
	err := db.Writer.Submit(ctx, "test_failure", func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `CREATE TRIGGER fail_promotion BEFORE INSERT ON object_revisions WHEN NEW.operation='promote' BEGIN SELECT RAISE(ABORT,'injected failure'); END`)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Promote(ctx, req, "test"); err == nil {
		t.Fatal("injected failure ignored")
	}
	if _, err = s.Get(ctx, req.TargetID); !errors.Is(err, ErrNotFound) {
		t.Fatal("target leaked", err)
	}
	if _, err = s.GetPromotion(ctx, source.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("receipt leaked", err)
	}
	current, err := s.Get(ctx, source.ID)
	if err != nil || current.Revision != source.Revision {
		t.Fatal("source changed", err)
	}
	links, err := s.Links(ctx, source.ID, false)
	if err != nil || len(links) != 0 {
		t.Fatal("links leaked", err)
	}
	err = db.Writer.Submit(ctx, "remove_failure", func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DROP TRIGGER fail_promotion`)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Promote(ctx, req, "test"); err != nil {
		t.Fatal("retry after rollback", err)
	}
}

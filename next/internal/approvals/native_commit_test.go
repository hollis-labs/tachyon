package approvals

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/hollis-labs/libs/mesh/core/gate"
	"github.com/hollis-labs/libs/mesh/core/gate/gatetest"
	"github.com/hollis-labs/libs/mesh/core/hitl"
	"github.com/hollis-labs/tachyon/next/internal/store"
	"github.com/hollis-labs/tachyon/next/internal/store/storetest"
	"testing"
	"time"
)

func TestNativeAuthorizationRequiresWallMonotonicAndCallerBudget(t *testing.T) {
	now := time.Now()
	valid := nativeAuthorization{Proposal: ProposedResponse{ExpiresAt: now.Add(time.Minute)}, BudgetDeadline: now.Add(time.Second)}
	if err := checkNativeAuthorization(context.Background(), valid); err != nil {
		t.Fatal(err)
	}
	elapsed := valid
	elapsed.BudgetDeadline = now.Add(-time.Second)
	if err := checkNativeAuthorization(context.Background(), elapsed); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("wall-valid but elapsed monotonic budget: %v", err)
	}
	expired := valid
	expired.Proposal.ExpiresAt = now.Add(-time.Second)
	if err := checkNativeAuthorization(context.Background(), expired); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("elapsed wall authorization: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := checkNativeAuthorization(ctx, valid); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled caller: %v", err)
	}
}

// Real service transitions create every outcome. Removing only private receipts
// models an upgrade from pre-confirmation records, and losing derived tables
// exercises maintenance without manufacturing any terminal choice.
func TestNativeQuarantinePagesAndBackfillProgress(t *testing.T) {
	ctx := context.Background()
	owner := NewInbox(nativeStore(t), nil)
	source := NewInbox(nativeStore(t), nil)
	legacyHandle, err := source.Service.Enqueue(ctx, requestFixture(t, "maintenance-legacy", "low"))
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := source.Store.Get(ctx, legacyHandle.ItemID)
	if err != nil {
		t.Fatal(err)
	}
	legacy.ItemID = "zz_maintenance_legacy"
	if _, _, err = owner.Store.Create(ctx, legacy); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 501; i++ {
		now := time.Now()
		n := gatetest.Request(now.UTC())
		n.IdempotencyKey = fmt.Sprintf("quarantine-%03d", i)
		raw, _ := json.Marshal(n)
		text, _ := json.Marshal(string(raw))
		q := hitl.EnqueueRequest{ContractVersion: hitl.ContractVersion, Kind: NativeKind, IdempotencyKey: n.IdempotencyKey, Source: hitl.SourceAssertion{ApplicationID: "gatecrasher", AgentID: string(n.Binding.OperativeID)}, ExpiresAt: &n.ExpiresAt, Extra: map[string]json.RawMessage{"gate_request": text}}
		h, enqueueErr := owner.Service.Enqueue(ctx, q)
		if enqueueErr != nil {
			t.Fatal(enqueueErr)
		}
		participant := hitl.Participant{PrincipalRef: "test-operator", Assurance: "operator-token"}
		digest, _ := n.Digest()
		p := ProposedResponse{ContractVersion: gate.ContractVersion, OwnerRef: "test-owner", IdempotencyKey: n.IdempotencyKey, ItemID: h.ItemID, RequestDigest: digest, OperationDigest: n.OperationDigest, Binding: n.Binding, NativeRequestID: n.NativeRequestID, ExpectedRevision: h.Revision, PresentedRevision: h.Revision, OptionID: n.Options[0].ID, Participant: participant, ActorScopes: []string{"tachyon.operator", "tachyon.approvals.resolve"}, AssessedAt: now.UTC(), ExpiresAt: boundedExpiry(now.UTC(), n, n.Options[0])}
		authorized := context.WithValue(ctx, nativeAuthorizationKey{}, nativeAuthorization{Proposal: p, BudgetDeadline: now.Add(p.ExpiresAt.Sub(p.AssessedAt))})
		if _, respondErr := owner.Service.Respond(authorized, hitl.RespondCommand{ItemID: h.ItemID, ExpectedRevision: &h.Revision, Response: hitl.Response{Kind: NativeKind, Decision: p.OptionID}, Participant: participant}); respondErr != nil {
			t.Fatal(respondErr)
		}
		if i == 0 {
			rec, canonical, evidence, readErr := readStoredRecord(owner.Store.DB.Reader.QueryRowContext(ctx, "SELECT record FROM approval_items WHERE id=?", h.ItemID))
			if readErr != nil {
				t.Fatal(readErr)
			}
			var e nativeCommitEvidence
			if json.Unmarshal(evidence, &e) != nil {
				t.Fatal("witness decode")
			}
			if refused, proofErr := nativeConfirmationBytes(rec, canonical, e, rec.UpdatedAt.Add(-time.Nanosecond)); !errors.Is(proofErr, ErrNativeUnconfirmed) || len(refused) != 0 {
				t.Fatalf("backwards observation publishable: %s %v", refused, proofErr)
			}
			if valid, proofErr := nativeConfirmationBytes(rec, canonical, e, *e.ObservedAt); proofErr != nil || !bytes.Equal(valid, evidence) {
				t.Fatalf("valid observation changed: %v", proofErr)
			}
			confirmedRecord, _ := recordBytesWithEvidence(rec, evidence)
			e.ObservedAt = nil
			initialEvidence, _ := json.Marshal(e)
			initialRecord, _ := recordBytesWithEvidence(rec, initialEvidence)
			if confirmErr := owner.Store.DB.Writer.Submit(ctx, "test_identical_witness", func(ctx context.Context, tx store.Tx) error {
				return confirmNativeRecord(ctx, tx, h.ItemID, rec.Revision, initialRecord, confirmedRecord)
			}); confirmErr != nil {
				t.Fatalf("identical witness not idempotent: %v", confirmErr)
			}
			var other nativeCommitEvidence
			if json.Unmarshal(evidence, &other) != nil {
				t.Fatal("witness decode")
			}
			moved := other.ObservedAt.Add(time.Nanosecond)
			other.ObservedAt = &moved
			differentEvidence, _ := json.Marshal(other)
			different, _ := recordBytesWithEvidence(rec, differentEvidence)
			if confirmErr := owner.Store.DB.Writer.Submit(ctx, "test_different_witness", func(ctx context.Context, tx store.Tx) error {
				return confirmNativeRecord(ctx, tx, h.ItemID, rec.Revision, initialRecord, different)
			}); !errors.Is(confirmErr, ErrNativeUnconfirmed) {
				t.Fatalf("different witness accepted: %v", confirmErr)
			}
		}
	}
	if _, err = owner.Service.Respond(ctx, hitl.RespondCommand{ItemID: legacy.ItemID, Response: hitl.Response{Kind: "approval", Decision: "approved"}, Participant: hitl.Participant{PrincipalRef: "operator", Assurance: "browser-session"}}); err != nil {
		t.Fatal(err)
	}
	rows, err := owner.Store.DB.Reader.QueryContext(ctx, "SELECT record FROM approval_items WHERE id<>?", legacy.ItemID)
	if err != nil {
		t.Fatal(err)
	}
	var old []hitl.Record
	for rows.Next() {
		rec, readErr := readRecord(rows)
		if readErr != nil {
			t.Fatal(readErr)
		}
		old = append(old, rec)
	}
	_ = rows.Close()
	err = owner.Store.DB.Writer.Submit(ctx, "test_upgrade_and_lost_projections", func(ctx context.Context, tx store.Tx) error {
		for _, rec := range old {
			raw, encodeErr := recordBytes(rec)
			if encodeErr != nil {
				return encodeErr
			}
			if _, writeErr := tx.ExecContext(ctx, "UPDATE approval_items SET record=? WHERE id=?", raw, rec.ItemID); writeErr != nil {
				return writeErr
			}
		}
		drop := "DROP TRIGGER approval_decision_log_no_delete"
		if storetest.Postgres() {
			drop += " ON approval_decision_log"
		}
		for _, q := range []string{drop, "DELETE FROM approval_decision_log", "DELETE FROM approval_item_index"} {
			if _, writeErr := tx.ExecContext(ctx, q); writeErr != nil {
				return writeErr
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	page, cursor, err := owner.Store.Page(ctx, true, "", 2)
	if err != nil || len(page) != 0 || cursor == "" {
		t.Fatalf("quarantine-only raw page did not advance: %v %q %v", page, cursor, err)
	}
	found := false
	for cursor != "" {
		page, cursor, err = owner.Store.Page(ctx, true, cursor, 2)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range page {
			if r.ItemID != legacy.ItemID {
				t.Fatal("quarantined page leaked outcome")
			}
			found = true
		}
	}
	if !found {
		t.Fatal("quarantine hid later legacy page")
	}
	if _, err = owner.Store.Reindex(ctx); err != nil {
		t.Fatal(err)
	}
	if refreshed, reindexErr := owner.Store.Reindex(ctx); reindexErr != nil || refreshed != 0 {
		t.Fatalf("unchanged quarantine is not a cache miss: %d %v", refreshed, reindexErr)
	}
	if _, err = owner.Store.BackfillLog(ctx); err != nil {
		t.Fatal(err)
	}
	events, _, err := owner.Store.Timeline(ctx, EventFilter{})
	if err != nil || len(events) != 1 || events[0].ItemID != legacy.ItemID {
		t.Fatalf("backfill did not advance past full quarantined batch: %v %v", events, err)
	}
}

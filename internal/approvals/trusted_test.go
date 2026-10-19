package approvals

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hollis-labs/libs/mesh/core/hitl"
	"github.com/hollis-labs/tachyon/internal/localstore"
)

type completionVerifierFunc func(context.Context, CompletionLookup) (json.RawMessage, error)

func (f completionVerifierFunc) VerifyCompletion(ctx context.Context, lookup CompletionLookup) (json.RawMessage, error) {
	return f(ctx, lookup)
}

type completionFixture struct {
	Case struct {
		Operation   json.RawMessage  `json:"operation"`
		Response    hitl.Response    `json:"response"`
		Participant hitl.Participant `json:"participant"`
	} `json:"case"`
}

func loadCompletionFixture(t *testing.T) completionFixture {
	t.Helper()
	raw, err := os.ReadFile("testdata/operation-proof-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture completionFixture
	if err = json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	return fixture
}

var proofNow = time.Unix(1900000010, 0)

// This stub tests the decision-storage boundary, NOT cryptographic verification.
// Shared library/Basalisk integrations must run the full interoperability suite.
func fixtureVerifier(t *testing.T, fixture completionFixture) CompletionVerifier {
	t.Helper()
	return completionVerifierFunc(func(_ context.Context, l CompletionLookup) (json.RawMessage, error) {
		if l.Authority != "human-root-example" || l.Audience != "credential-service-example" || l.CompletionID != "completion-example" || l.ItemID != "item-example-1" || l.ExpectedRevision != 1 {
			t.Errorf("incorrect authoritative lookup: %+v", l)
		}
		var request hitl.EnqueueRequest
		if err := json.Unmarshal(l.RequestJSON, &request); err != nil || extraString(request, "class") != "high" {
			t.Errorf("missing authoritative request: %v", err)
		}
		return fixture.Case.Response.Extra["assurance_evidence"], nil
	})
}
func enqueueCompletion(t *testing.T, store *Store, fixture completionFixture, id string, mutate func(*hitl.EnqueueRequest)) {
	t.Helper()
	req := requestFixture(t, id, "high")
	req.Extra["operation"] = fixture.Case.Operation
	var evidence assuranceEvidence
	if err := json.Unmarshal(fixture.Case.Response.Extra["assurance_evidence"], &evidence); err != nil {
		t.Fatal(err)
	}
	req.Extra["op_digest"], _ = json.Marshal(evidence.Binding.OpDigest)
	if mutate != nil {
		mutate(&req)
	}
	service := hitl.NewService(store, hitl.Options{NewID: func() string { return id }, Clock: func() time.Time { return proofNow }})
	if _, err := service.Enqueue(context.Background(), req); err != nil {
		t.Fatal(err)
	}
}
func adapterFixture(t *testing.T, store *Store, verifier CompletionVerifier) *TrustedDecisions {
	t.Helper()
	adapter, err := NewTrustedDecisions(store, "human-root-example", map[string]string{"test-producer": "credential-service-example"}, verifier)
	if err != nil {
		t.Fatal(err)
	}
	adapter.now = func() time.Time { return proofNow }
	return adapter
}
func completionCommand() CompletionCommand {
	return CompletionCommand{ItemID: "item-example-1", CompletionID: "completion-example", ExpectedRevision: 1}
}
func assertPendingWithoutReceipt(t *testing.T, store *Store, id string) {
	t.Helper()
	rec, err := store.Get(context.Background(), id)
	if err != nil || rec.State != hitl.StatePresented {
		t.Fatalf("request changed: %+v %v", rec, err)
	}
	var n int
	if err = store.DB.Reader.QueryRow("SELECT count(*) FROM approval_proof_receipts").Scan(&n); err != nil || n != 0 {
		t.Fatalf("unexpected receipt: %d %v", n, err)
	}
}

func TestTrustedCompletionDurableConcurrentRetry(t *testing.T) {
	fixture := loadCompletionFixture(t)
	path := filepath.Join(t.TempDir(), "approvals.db")
	db, err := localstore.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	store := &Store{DB: db}
	defer func() { _ = store.DB.Close() }()
	enqueueCompletion(t, store, fixture, "item-example-1", nil)
	adapter := adapterFixture(t, store, fixtureVerifier(t, fixture))
	var wg sync.WaitGroup
	results := make(chan hitl.Outcome, 12)
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out, err := adapter.Complete(context.Background(), completionCommand())
			if err != nil {
				t.Errorf("completion: %v", err)
				return
			}
			results <- out
		}()
	}
	wg.Wait()
	close(results)
	var first string
	for out := range results {
		raw, _ := json.Marshal(out)
		if first == "" {
			first = string(raw)
		} else if string(raw) != first {
			t.Fatal("retry created another outcome")
		}
	}
	if first == "" {
		t.Fatal("no completion")
	}
	var n int
	if err = db.Reader.QueryRow("SELECT count(*) FROM approval_proof_receipts").Scan(&n); err != nil || n != 1 {
		t.Fatalf("receipt count %d: %v", n, err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = localstore.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	store.DB = db
	adapter = adapterFixture(t, store, fixtureVerifier(t, fixture))
	out, err := adapter.Complete(context.Background(), completionCommand())
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(out)
	if string(raw) != first {
		t.Fatal("restart retry changed outcome")
	}
	resolved := out.(hitl.Resolved)
	var got, want any
	_ = json.Unmarshal(resolved.Resolution.Response.Extra["assurance_evidence"], &got)
	_ = json.Unmarshal(fixture.Case.Response.Extra["assurance_evidence"], &want)
	a, _ := json.Marshal(got)
	b, _ := json.Marshal(want)
	if string(a) != string(b) {
		t.Fatal("evidence changed")
	}
	p, _ := json.Marshal(resolved.Resolution.Participant)
	q, _ := json.Marshal(fixture.Case.Participant)
	if string(p) != string(q) {
		t.Fatalf("participant changed: %s", p)
	}
	// Successful delivery does not bypass current trust checks on later retries.
	adapter.verifier = completionVerifierFunc(func(context.Context, CompletionLookup) (json.RawMessage, error) {
		return nil, errors.New("key revoked")
	})
	if _, err = adapter.Complete(context.Background(), completionCommand()); !errors.Is(err, ErrCompletionUnavailable) {
		t.Fatalf("revoked trust accepted: %v", err)
	}
}

func TestTrustedCompletionRejectsBindingAndParserErrors(t *testing.T) {
	for _, field := range []string{"profile", "authority", "audience", "item_id", "op_digest", "operation_id", "operation_revision", "decision", "nonce", "issued_at", "expires_at", "human_subject", "key_ref", "proof_id", "duplicate", "unknown", "null", "oversize", "invalid_utf8", "bad_assertion"} {
		t.Run(field, func(t *testing.T) {
			fixture := loadCompletionFixture(t)
			store := nativeStore(t)
			enqueueCompletion(t, store, fixture, "item-example-1", nil)
			original := fixture.Case.Response.Extra["assurance_evidence"]
			var value map[string]any
			_ = json.Unmarshal(original, &value)
			binding := value["binding"].(map[string]any)
			switch field {
			case "human_subject", "key_ref", "proof_id":
				binding[field] = ""
			case "issued_at":
				binding[field] = "1900000011"
			case "expires_at":
				binding[field] = "1900000010" // exact expiry refuses
			case "unknown":
				binding["future_field"] = "unsupported"
			case "null":
				binding["proof_id"] = nil
			case "bad_assertion":
				value["assertion"].(map[string]any)["signature"] = "***"
			default:
				binding[field] = "wrong"
			}
			raw, _ := json.Marshal(value)
			if field == "duplicate" {
				raw = []byte(strings.Replace(string(original), `"profile":`, `"profile":"duplicate","profile":`, 1))
			}
			if field == "oversize" {
				raw = append(raw, []byte(strings.Repeat(" ", 32<<10))...)
			}
			if field == "invalid_utf8" {
				raw = append(raw, 0xff)
			}
			fixture.Case.Response.Extra["assurance_evidence"] = raw
			adapter := adapterFixture(t, store, fixtureVerifier(t, fixture))
			if _, err := adapter.Complete(context.Background(), completionCommand()); err == nil {
				t.Fatal("invalid completion accepted")
			}
			assertPendingWithoutReceipt(t, store, "item-example-1")
		})
	}
}

func TestTrustedCompletionRefusesUntrustedInputs(t *testing.T) {
	for _, name := range []string{"unmapped", "cerberus", "low", "no_operation", "self_approval", "wrong_operation_audience", "stale_revision", "verifier_error", "empty_evidence", "item_expired"} {
		t.Run(name, func(t *testing.T) {
			fixture := loadCompletionFixture(t)
			store := nativeStore(t)
			enqueueCompletion(t, store, fixture, "item-example-1", func(req *hitl.EnqueueRequest) {
				switch name {
				case "unmapped":
					req.Source.ApplicationID = "other"
				case "cerberus":
					req.Source.ApplicationID = "cerberus"
				case "low":
					req.Extra["class"] = json.RawMessage(`"low"`)
				case "no_operation":
					delete(req.Extra, "operation")
				case "self_approval", "wrong_operation_audience":
					var op map[string]any
					_ = json.Unmarshal(req.Extra["operation"], &op)
					if name == "self_approval" {
						op["requester"] = map[string]string{"authority": "human-root-example", "subject": "human-example"}
					} else {
						op["audience"] = "another-app"
					}
					req.Extra["operation"], _ = json.Marshal(op)
				case "item_expired":
					until := proofNow.Add(-time.Second)
					req.ExpiresAt = &until
				}
			})
			adapter := adapterFixture(t, store, fixtureVerifier(t, fixture))
			cmd := completionCommand()
			if name == "stale_revision" {
				cmd.ExpectedRevision = 2
				adapter.verifier = completionVerifierFunc(func(context.Context, CompletionLookup) (json.RawMessage, error) {
					return fixture.Case.Response.Extra["assurance_evidence"], nil
				})
			}
			if name == "verifier_error" {
				adapter.verifier = completionVerifierFunc(func(context.Context, CompletionLookup) (json.RawMessage, error) {
					return nil, errors.New("unavailable")
				})
			}
			if name == "empty_evidence" {
				adapter.verifier = completionVerifierFunc(func(context.Context, CompletionLookup) (json.RawMessage, error) { return nil, nil })
			}
			if _, err := adapter.Complete(context.Background(), cmd); err == nil {
				t.Fatal("untrusted completion accepted")
			}
			assertPendingWithoutReceipt(t, store, "item-example-1")
		})
	}
	var disabled *TrustedDecisions
	if _, err := disabled.Complete(context.Background(), completionCommand()); !errors.Is(err, ErrCompletionUnavailable) {
		t.Fatal(err)
	}
	if _, err := NewTrustedDecisions(nativeStore(t), "root", map[string]string{"tether": "tether"}, nil); !errors.Is(err, ErrCompletionUnavailable) {
		t.Fatal("nil verifier accepted")
	}
}

func TestTrustedCompletionReceiptRollback(t *testing.T) {
	fixture := loadCompletionFixture(t)
	store := nativeStore(t)
	enqueueCompletion(t, store, fixture, "item-example-1", nil)
	err := store.DB.Writer.Submit(context.Background(), "test_receipt_failure", func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `CREATE TRIGGER reject_proof_receipt BEFORE INSERT ON approval_proof_receipts BEGIN SELECT RAISE(ABORT,'injected receipt failure'); END`)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	adapter := adapterFixture(t, store, fixtureVerifier(t, fixture))
	if _, err = adapter.Complete(context.Background(), completionCommand()); err == nil {
		t.Fatal("injected failure accepted")
	}
	assertPendingWithoutReceipt(t, store, "item-example-1")
	err = store.DB.Writer.Submit(context.Background(), "test_receipt_recovery", func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "DROP TRIGGER reject_proof_receipt")
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = adapter.Complete(context.Background(), completionCommand()); err != nil {
		t.Fatal(err)
	}
}

func TestTrustedCompletionProofReuseAndChangedRetry(t *testing.T) {
	fixture := loadCompletionFixture(t)
	store := nativeStore(t)
	enqueueCompletion(t, store, fixture, "item-example-1", nil)
	adapter := adapterFixture(t, store, fixtureVerifier(t, fixture))
	if _, err := adapter.Complete(context.Background(), completionCommand()); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"different_item", "changed_evidence", "changed_completion"} {
		t.Run(mode, func(t *testing.T) {
			var evidence assuranceEvidence
			_ = json.Unmarshal(fixture.Case.Response.Extra["assurance_evidence"], &evidence)
			cmd := completionCommand()
			switch mode {
			case "different_item":
				cmd.ItemID = "item-example-2"
				evidence.Binding.ItemID = cmd.ItemID
				enqueueCompletion(t, store, fixture, cmd.ItemID, nil)
			case "changed_evidence":
				evidence.Assertion.Signature = "YWJj"
			case "changed_completion":
				cmd.CompletionID = "another-completion"
			}
			raw, _ := json.Marshal(evidence)
			adapter.verifier = completionVerifierFunc(func(context.Context, CompletionLookup) (json.RawMessage, error) { return raw, nil })
			if _, err := adapter.Complete(context.Background(), cmd); !errors.Is(err, ErrCompletionConflict) {
				t.Fatalf("reused proof accepted: %v", err)
			}
			if mode == "different_item" {
				rec, _ := store.Get(context.Background(), cmd.ItemID)
				if rec.State != hitl.StatePresented {
					t.Fatal("replay resolved other item")
				}
			}
		})
	}
}

func TestTrustedCompletionExpiryAtWriterBoundary(t *testing.T) {
	fixture := loadCompletionFixture(t)
	store := nativeStore(t)
	enqueueCompletion(t, store, fixture, "item-example-1", nil)
	adapter := adapterFixture(t, store, fixtureVerifier(t, fixture))
	writerEntered, releaseWriter, verified := make(chan struct{}), make(chan struct{}), make(chan struct{})
	blocker := make(chan error, 1)
	go func() {
		blocker <- store.DB.Writer.Submit(context.Background(), "test_hold_writer", func(context.Context, *sql.Tx) error { close(writerEntered); <-releaseWriter; return nil })
	}()
	<-writerEntered
	adapter.verifier = completionVerifierFunc(func(context.Context, CompletionLookup) (json.RawMessage, error) {
		close(verified)
		return fixture.Case.Response.Extra["assurance_evidence"], nil
	})
	// The writer is unavailable until after proof expiry. The adapter's clock
	// is only consulted in its transaction, not at pre-queue verification time.
	adapter.now = func() time.Time { return time.Unix(1900000300, 0) }
	done := make(chan error, 1)
	go func() { _, err := adapter.Complete(context.Background(), completionCommand()); done <- err }()
	<-verified
	close(releaseWriter)
	if err := <-blocker; err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, hitl.ErrUnauthorized) {
		t.Fatalf("late decision: %v", err)
	}
	assertPendingWithoutReceipt(t, store, "item-example-1")
}

func TestTrustedCompletionCompetesWithWithdrawal(t *testing.T) {
	fixture := loadCompletionFixture(t)
	store := nativeStore(t)
	enqueueCompletion(t, store, fixture, "item-example-1", nil)
	adapter := adapterFixture(t, store, fixtureVerifier(t, fixture))
	service := hitl.NewService(store, hitl.Options{Clock: func() time.Time { return proofNow }})
	start := make(chan struct{})
	done := make(chan error, 2)
	go func() { <-start; _, err := adapter.Complete(context.Background(), completionCommand()); done <- err }()
	go func() {
		<-start
		_, err := service.Withdraw(context.Background(), hitl.WithdrawCommand{ContractVersion: hitl.ContractVersion, ItemID: "item-example-1", Caller: hitl.CallerAssertion{ApplicationID: "test-producer"}})
		done <- err
	}()
	close(start)
	successes := 0
	for range 2 {
		if err := <-done; err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("terminal winners: %d", successes)
	}
	rec, err := store.Get(context.Background(), "item-example-1")
	if err != nil {
		t.Fatal(err)
	}
	var receipts int
	if err = store.DB.Reader.QueryRow("SELECT count(*) FROM approval_proof_receipts").Scan(&receipts); err != nil {
		t.Fatal(err)
	}
	if rec.State == hitl.StateResolved && receipts != 1 || rec.State == hitl.StateCanceled && receipts != 0 {
		t.Fatalf("non-atomic terminal state %s, receipts %d", rec.State, receipts)
	}
}

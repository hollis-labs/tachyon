package dataverbs

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/hollis-labs/tachyon/next/internal/approvals"
	"github.com/hollis-labs/tachyon/next/internal/store"
	"github.com/hollis-labs/tachyon/next/internal/store/storetest"
)

// Preserve the reviewer's real B-only index fault: confirmation and assessment
// succeed while only the derived native_allow update is rejected. Recovery must
// work at the SAME revision, including when its first pass is after reopening.
func TestNativeGateConfirmedIndexRecovery(t *testing.T) {
	for _, firstAfterReopen := range []bool{false, true} {
		name := "before_reopen"
		if firstAfterReopen {
			name = "first_after_reopen"
		}
		t.Run(name, func(t *testing.T) {
			f := nativeStack(t)
			n, h := f.enqueue("confirmed-index-" + name)
			ctx := context.Background()
			run := func(q string) {
				t.Helper()
				if err := f.inbox.Store.DB.Writer.Submit(ctx, "test_confirmed_index_fault", func(ctx context.Context, tx store.Tx) error { _, writeErr := tx.ExecContext(ctx, q); return writeErr }); err != nil {
					t.Fatal(err)
				}
			}
			if storetest.Postgres() {
				run(`CREATE FUNCTION test_fail_confirmed_index() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.status='native_allow' THEN RAISE EXCEPTION 'transient derived write failure'; END IF; RETURN NEW; END $$`)
				run(`CREATE TRIGGER test_fail_confirmed_index BEFORE UPDATE ON approval_item_index FOR EACH ROW EXECUTE FUNCTION test_fail_confirmed_index()`)
			} else {
				run(`CREATE TRIGGER test_fail_confirmed_index BEFORE UPDATE ON approval_item_index WHEN NEW.status='native_allow' BEGIN SELECT RAISE(ABORT,'transient derived write failure'); END`)
			}
			f.want(f.owner, 200, "POST", browserPath(h)+"/resolve", choice(h, n.Options[0].ID), "")
			f.want(f.anonymous, 200, "GET", gatePath("gate.assess-response", assessment(h)), nil, f.producer)
			original, evidence := retainedNativeEvidence(t, f.inbox.Store, h.ItemID)
			if !bytes.Contains(evidence, []byte(`"observed_at"`)) {
				t.Fatal("fault prevented authoritative confirmation")
			}
			var status string
			var revision int64
			if err := f.inbox.Store.DB.Reader.QueryRowContext(ctx, "SELECT status,revision FROM approval_item_index WHERE item_id=?", h.ItemID).Scan(&status, &revision); err != nil {
				t.Fatal(err)
			}
			if status != "unavailable" || revision != h.Revision+1 {
				t.Fatalf("fault did not preserve the phase-A index: %s %d", status, revision)
			}
			if storetest.Postgres() {
				run(`DROP TRIGGER test_fail_confirmed_index ON approval_item_index`)
			} else {
				run(`DROP TRIGGER test_fail_confirmed_index`)
			}
			owner := f.inbox.Store
			if firstAfterReopen {
				owner = reopenNativeOwner(t, f)
			}
			repaired, err := owner.Reindex(ctx)
			if err != nil || repaired != 1 {
				t.Fatalf("same-revision repair: %d %v", repaired, err)
			}
			if err = owner.DB.Reader.QueryRowContext(ctx, "SELECT status,revision FROM approval_item_index WHERE item_id=?", h.ItemID).Scan(&status, &revision); err != nil {
				t.Fatal(err)
			}
			ids, _, err := owner.History(ctx, approvals.Filter{Status: "native_allow"}, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			if status != "native_allow" || revision != h.Revision+1 || len(ids) != 1 || ids[0] != h.ItemID {
				t.Fatalf("confirmed projection not recovered: %s %d %v", status, revision, ids)
			}
			recovered, recoveredEvidence := retainedNativeEvidence(t, owner, h.ItemID)
			if !bytes.Equal(original, recovered) || !bytes.Equal(evidence, recoveredEvidence) {
				t.Fatal("derived recovery rewrote choice, native data or witness")
			}
			if !firstAfterReopen {
				owner = reopenNativeOwner(t, f)
			}
			if again, repairErr := owner.Reindex(ctx); repairErr != nil || again != 0 {
				t.Fatalf("reopened recovered index is not current: %d %v", again, repairErr)
			}
			reopened, _ := retainedNativeEvidence(t, owner, h.ItemID)
			if !bytes.Equal(original, reopened) {
				t.Fatal("reopen/reindex changed authoritative bytes")
			}
		})
	}
}

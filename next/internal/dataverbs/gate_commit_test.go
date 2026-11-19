package dataverbs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	libstore "github.com/hollis-labs/libs/core/store"
	"github.com/hollis-labs/libs/mesh/core/hitl"
	"github.com/hollis-labs/tachyon/next/internal/approvals"
	"github.com/hollis-labs/tachyon/next/internal/store"
	"github.com/hollis-labs/tachyon/next/internal/store/storetest"

	"modernc.org/sqlite"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Delay only the driver's actual COMMIT, after all owner checks and UPDATEs.
// Unlike a pre-transaction lock, this exposes whether Commit uses the deadline.
func TestNativeGateAuthorizationDeadlineDuringSQLiteCommit(t *testing.T) {
	if storetest.Postgres() {
		t.Skip("SQLite driver's commit context probe")
	}
	f := nativeStack(t)
	n, h := f.enqueue("review-commit-deadline")
	original := f.inbox.Native.Authorize
	var calls atomic.Int32
	var until time.Time
	f.inbox.Native.Authorize = func(ctx context.Context, p approvals.ProposedResponse) (approvals.ResponseAuthorization, error) {
		a, err := original(ctx, p)
		if err != nil {
			return a, err
		}
		until = a.ExpiresAt
		conn, err := f.inbox.Store.DB.Lib().DB().Conn(ctx)
		if err != nil {
			return a, err
		}
		err = conn.Raw(func(raw any) error {
			hook, ok := raw.(interface{ RegisterCommitHook(sqlite.CommitHookFn) })
			if !ok {
				return fmt.Errorf("driver has no commit hook")
			}
			hook.RegisterCommitHook(func() int32 {
				if calls.Add(1) == 1 {
					time.Sleep(time.Until(until.Add(250 * time.Millisecond)))
				}
				return 0
			})
			return nil
		})
		_ = conn.Close()
		return a, err
	}
	before := approvals.Changes(f.inbox.Store.DB).Head()
	status, body := f.do(f.owner, "POST", browserPath(h)+"/resolve", choice(h, n.Options[0].ID), "")
	if approvals.Changes(f.inbox.Store.DB).Head() != before {
		t.Fatal("unconfirmed terminal event published")
	}
	if calls.Load() != 1 {
		t.Fatal("did not reach exactly the original decision commit hook")
	}
	if status != 503 {
		t.Fatalf("late commit returned %d: %s", status, body)
	}
	ctx := context.Background()
	if _, err := f.inbox.Store.Get(ctx, h.ItemID); !errors.Is(err, approvals.ErrNativeUnconfirmed) {
		t.Fatalf("late owner read: %v", err)
	}
	raw, evidence := retainedNativeEvidence(t, f.inbox.Store, h.ItemID)
	var saved struct {
		State    hitl.State
		Revision int64
		Outcome  json.RawMessage
	}
	if json.Unmarshal(raw, &saved) != nil || saved.State != hitl.StateResolved || saved.Revision != h.Revision+1 || len(saved.Outcome) == 0 || bytes.Contains(evidence, []byte(`"observed_at"`)) {
		t.Fatalf("retained late physical outcome/evidence: %s %s", raw, evidence)
	}
	t.Logf("original actual-COMMIT hook retained a raw late resolution at %s; all authority reads must refuse", time.Now().UTC().Format(time.RFC3339Nano))
	assertNativeQuarantine(t, f, h)
	verifyNativeBackup(t, f, h, false)
	// Close the actual owner database and reopen its file. No witness can be
	// reconstructed from the retained outcome or timestamps after process loss.
	var seq int
	var dbname, path string
	if err := f.inbox.Store.DB.Reader.QueryRowContext(ctx, "PRAGMA database_list").Scan(&seq, &dbname, &path); err != nil {
		t.Fatal(err)
	}
	if err := f.inbox.Store.DB.Lib().Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := store.OpenFile(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	owner := &approvals.Store{DB: reopened}
	if _, err = owner.Get(ctx, h.ItemID); !errors.Is(err, approvals.ErrNativeUnconfirmed) {
		t.Fatalf("reopened late outcome usable: %v", err)
	}
	if _, err = owner.Reindex(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = owner.BackfillLog(ctx); err != nil {
		t.Fatal(err)
	}
	events, _, err := owner.Timeline(ctx, approvals.EventFilter{})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range events {
		if e.ItemID == h.ItemID {
			t.Fatal("reopened quarantine leaked an event")
		}
	}
}

func assertNativeQuarantine(t *testing.T, f *nativeFixture, h hitl.Handle) {
	t.Helper()
	ctx := context.Background()
	legacy, err := f.inbox.Service.Enqueue(ctx, hitl.EnqueueRequest{ContractVersion: hitl.ContractVersion, Kind: "approval", IdempotencyKey: "quarantine-legacy", Source: hitl.SourceAssertion{ApplicationID: "legacy", AgentID: "legacy-agent"}, Extra: map[string]json.RawMessage{"title": json.RawMessage(`"Legacy card remains usable"`), "class": json.RawMessage(`"low"`)}})
	if err != nil {
		t.Fatal(err)
	}
	for _, verb := range []string{"gate.get", "gate.assess-response"} {
		var q any = getCommand(h)
		if verb == "gate.assess-response" {
			q = assessment(h)
		}
		b := f.want(f.anonymous, 503, "GET", gatePath(verb, q), nil, f.producer)
		if bytes.Contains(b, []byte(`"outcome"`)) {
			t.Fatalf("error leaked outcome: %s", b)
		}
	}
	var retained hitl.EnqueueRequest
	var raw []byte
	if err = f.inbox.Store.DB.Reader.QueryRowContext(ctx, "SELECT record FROM approval_items WHERE id=?", h.ItemID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var disk struct{ Request []byte }
	if json.Unmarshal(raw, &disk) != nil || json.Unmarshal(disk.Request, &retained) != nil {
		t.Fatal("retained request decode")
	}
	f.want(f.anonymous, 503, "POST", gatePath("gate.enqueue", nil), retained, f.producer)
	f.want(f.anonymous, 503, "POST", gatePath("gate.withdraw", nil), hitl.WithdrawCommand{ContractVersion: hitl.ContractVersion, ItemID: h.ItemID, Caller: getCommand(h).Caller}, f.producer)
	aq := hitl.AwaitCommand{ContractVersion: hitl.ContractVersion, ItemID: h.ItemID, Caller: getCommand(h).Caller}
	f.want(f.anonymous, 503, "GET", gatePath("gate.await", aq), nil, f.producer)
	f.want(f.owner, 503, "GET", browserPath(h), nil, "")
	f.want(f.owner, 503, "POST", browserPath(h)+"/resolve", choice(h, "native-deny"), "")
	rec := f.inbox.Service
	if out, err := rec.Respond(ctx, hitl.RespondCommand{ItemID: h.ItemID, Response: hitl.Response{Kind: approvals.NativeKind, Decision: "native-deny"}, Participant: hitl.Participant{PrincipalRef: "user:owner", Assurance: "operator-token"}}); err == nil || out != nil {
		t.Fatalf("response replay produced authority: %v %v", out, err)
	}
	if _, err := f.inbox.Store.Reindex(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := f.inbox.Store.BackfillLog(ctx); err != nil {
		t.Fatal(err)
	}
	b := f.want(f.owner, 200, "GET", approvals.Prefix+"/inbox", nil, "")
	if !bytes.Contains(b, []byte(legacy.ItemID)) {
		t.Fatalf("quarantine blocked unrelated legacy card: %s", b)
	}
	if !bytes.Contains(b, []byte(`"native_unavailable"`)) || bytes.Contains(b, []byte(`"terminal_outcome"`)) {
		t.Fatalf("missing reconciliation status or outcome leak: %s", b)
	}
	b = f.want(f.owner, 200, "GET", approvals.Prefix+"/history", nil, "")
	if bytes.Contains(b, []byte(h.ItemID)) {
		t.Fatalf("quarantined native history leak: %s", b)
	}
	if events, err := f.inbox.Store.ItemEvents(ctx, h.ItemID); !errors.Is(err, approvals.ErrNativeUnconfirmed) || len(events) != 0 {
		t.Fatalf("item events: %v %v", events, err)
	}
	if rows, _, err := f.inbox.Store.Page(ctx, true, "", 100); err != nil {
		t.Fatal(err)
	} else {
		for _, r := range rows {
			if r.ItemID == h.ItemID {
				t.Fatal("page leaked quarantined outcome")
			}
		}
	}
}

// Fail phase two inside the actual owner's database, after a real on-time choice
// commit. This models a lost confirmation without seeding a terminal outcome.
func TestNativeGateConfirmationFailure(t *testing.T) {
	f := nativeStack(t)
	ctx := context.Background()
	refuseNativeConfirmation(t, f)
	n, h := f.enqueue("confirmation-failure")
	f.want(f.owner, 503, "POST", browserPath(h)+"/resolve", choice(h, n.Options[0].ID), "")
	assertNativeQuarantine(t, f, h)
	if os.Getenv("TACHYON_NATIVE_BROWSER") == "1" {
		cmd := exec.Command("node", "../../web/e2e/native-gate-browser.mjs")
		cmd.Env = append(os.Environ(), "NATIVE_TEST_BASE="+f.base, "NATIVE_TEST_ITEM="+h.ItemID, "NATIVE_TEST_PASSWORD="+nativePassword, "NATIVE_TEST_QUARANTINED=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("quarantine real browser: %v %s", err, out)
		}
	}
	verifyNativeBackup(t, f, h, false)
	reopened := reopenNativeOwner(t, f)
	if _, err := reopened.Get(ctx, h.ItemID); !errors.Is(err, approvals.ErrNativeUnconfirmed) {
		t.Fatalf("lost confirmation repaired itself on reopen: %v", err)
	}
}

func reopenNativeOwner(t *testing.T, f *nativeFixture) *approvals.Store {
	t.Helper()
	ctx := context.Background()
	cfg := nativeOwnerConfig(t, f)
	if err := f.inbox.Store.DB.Lib().Close(); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return &approvals.Store{DB: db}
}

func TestNativeGateConfirmedReopen(t *testing.T) {
	f := nativeStack(t)
	n, h := f.enqueue("confirmed-reopen")
	f.want(f.owner, 200, "POST", browserPath(h)+"/resolve", choice(h, n.Options[0].ID), "")
	f.want(f.anonymous, 200, "GET", gatePath("gate.assess-response", assessment(h)), nil, f.producer)
	verifyNativeBackup(t, f, h, true)
	reopened := reopenNativeOwner(t, f)
	rec, err := reopened.Get(context.Background(), h.ItemID)
	if err != nil || rec.Outcome == nil || rec.Revision != h.Revision+1 {
		t.Fatalf("confirmed restart: %+v %v", rec, err)
	}
	inbox := approvals.NewInbox(reopened, nil)
	result, err := inbox.Service.Get(context.Background(), getCommand(h))
	if err != nil || result.Item.TerminalOutcome == nil {
		t.Fatalf("confirmed retrieval after restart: %+v %v", result, err)
	}
}

// A real process dies at the second driver's COMMIT hook. Phase one is already
// durable and on time, but no committing process survives to finish its witness.
func TestNativeGateCommitCrash(t *testing.T) {
	if storetest.Postgres() {
		t.Skip("deterministic SQLite driver process-crash hook; both dialects cover phase-two store failure/reopen")
	}
	if report := os.Getenv("TACHYON_NATIVE_COMMIT_CRASH_REPORT"); report != "" {
		f := nativeStack(t)
		n, h := f.enqueue("crash-gap")
		ctx := context.Background()
		var seq int
		var name, path string
		if err := f.inbox.Store.DB.Reader.QueryRowContext(ctx, "PRAGMA database_list").Scan(&seq, &name, &path); err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(struct {
			Path   string
			Handle hitl.Handle
		}{path, h})
		if err := os.WriteFile(report, raw, 0600); //nolint:gosec // parent test chooses its own private crash-report path
		err != nil {
			t.Fatal(err)
		}
		original := f.inbox.Native.Authorize
		var commits atomic.Int32
		f.inbox.Native.Authorize = func(ctx context.Context, p approvals.ProposedResponse) (approvals.ResponseAuthorization, error) {
			a, err := original(ctx, p)
			if err != nil {
				return a, err
			}
			conn, err := f.inbox.Store.DB.Lib().DB().Conn(ctx)
			if err != nil {
				return a, err
			}
			err = conn.Raw(func(raw any) error {
				raw.(interface{ RegisterCommitHook(sqlite.CommitHookFn) }).RegisterCommitHook(func() int32 {
					at := commits.Add(1)
					phase := os.Getenv("TACHYON_NATIVE_COMMIT_CRASH_PHASE")
					if phase == "phase_one" && at == 1 {
						os.Exit(72)
					}
					if phase != "confirmed" && at == 2 {
						os.Exit(73)
					}
					return 0
				})
				return nil
			})
			_ = conn.Close()
			return a, err
		}
		f.want(f.owner, 200, "POST", browserPath(h)+"/resolve", choice(h, n.Options[0].ID), "")
		if os.Getenv("TACHYON_NATIVE_COMMIT_CRASH_PHASE") == "confirmed" {
			os.Exit(74)
		}
		t.Fatal("process never reached requested crash boundary")
	}
	for _, phase := range []string{"phase_one", "gap", "confirmed"} {
		t.Run(phase, func(t *testing.T) {
			dir := t.TempDir()
			report := filepath.Join(dir, "crash.json")
			child := exec.Command(os.Args[0], "-test.run=^TestNativeGateCommitCrash$", "-test.v") //nolint:gosec // re-exec this test binary with fixed test-only arguments
			child.Env = append(os.Environ(), "TACHYON_NATIVE_COMMIT_CRASH_REPORT="+report, "TMPDIR="+dir, "TACHYON_NATIVE_COMMIT_CRASH_PHASE="+phase)
			output, err := child.CombinedOutput()
			var exit *exec.ExitError
			expected := 73
			if phase == "phase_one" {
				expected = 72
			}
			if phase == "confirmed" {
				expected = 74
			}
			if !errors.As(err, &exit) || exit.ExitCode() != expected {
				t.Fatalf("crash subprocess: %v %s", err, output)
			}
			raw, err := os.ReadFile(report) //nolint:gosec // report is inside this test's private directory
			if err != nil {
				t.Fatal(err)
			}
			var saved struct {
				Path   string
				Handle hitl.Handle
			}
			if json.Unmarshal(raw, &saved) != nil {
				t.Fatal("invalid crash report")
			}
			db, err := store.OpenFile(context.Background(), saved.Path)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			owner := &approvals.Store{DB: db}
			raw, evidence := retainedNativeEvidence(t, owner, saved.Handle.ItemID)
			var rec struct{ State string }
			if json.Unmarshal(raw, &rec) != nil {
				t.Fatal("invalid retained record")
			}
			state := rec.State
			recOut, readErr := owner.Get(context.Background(), saved.Handle.ItemID)
			switch phase {
			case "phase_one":
				if readErr != nil || recOut.Outcome != nil || recOut.State.IsTerminal() {
					t.Fatalf("crash before first COMMIT published choice: %+v %v", recOut, readErr)
				}
			case "gap":
				if state != "resolved" || bytes.Contains(evidence, []byte(`"observed_at"`)) || !errors.Is(readErr, approvals.ErrNativeUnconfirmed) {
					t.Fatalf("crash gap fabricated witness: %s %s %v", state, evidence, readErr)
				}
			case "confirmed":
				if readErr != nil || recOut.Outcome == nil || !bytes.Contains(evidence, []byte(`"observed_at"`)) {
					t.Fatalf("crash lost durable witness: %+v %v", recOut, readErr)
				}
			}

			if _, err = owner.Reindex(context.Background()); err != nil {
				t.Fatal(err)
			}
			if _, err = owner.BackfillLog(context.Background()); err != nil {
				t.Fatal(err)
			}
			events, _, err := owner.Timeline(context.Background(), approvals.EventFilter{})
			if err != nil || (phase != "confirmed" && len(events) != 0) || (phase == "confirmed" && len(events) != 1) {
				t.Fatalf("crash derived outcome leak: %v %v", events, err)
			}
		})
	}

}

func retainedNativeEvidence(t *testing.T, owner *approvals.Store, id string) ([]byte, []byte) {
	t.Helper()
	var raw []byte
	if err := owner.DB.Reader.QueryRowContext(context.Background(), "SELECT record FROM approval_items WHERE id=?", id).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var saved struct {
		Native json.RawMessage `json:"native_commit"`
	}
	if json.Unmarshal(raw, &saved) != nil {
		t.Fatal("invalid retained owner envelope")
	}
	return raw, saved.Native
}

func TestNativeGateRejectsMissingOrMismatchedWitness(t *testing.T) {
	f := nativeStack(t)
	n, h := f.enqueue("witness-integrity")
	f.want(f.owner, 200, "POST", browserPath(h)+"/resolve", choice(h, n.Options[0].ID), "")
	original, _ := retainedNativeEvidence(t, f.inbox.Store, h.ItemID)
	// An unchanged envelope must survive the mutation fixture's codec. Otherwise
	// numeric rounding can make every negative case fail for the wrong reason.
	var unchanged map[string]any
	controlDecoder := json.NewDecoder(bytes.NewReader(original))
	controlDecoder.UseNumber()
	if err := controlDecoder.Decode(&unchanged); err != nil {
		t.Fatal(err)
	}
	roundtrip, err := json.Marshal(unchanged)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.inbox.Store.DB.Writer.Submit(context.Background(), "test_witness_roundtrip_control", func(ctx context.Context, tx store.Tx) error {
		_, writeErr := tx.ExecContext(ctx, "UPDATE approval_items SET record=? WHERE id=?", roundtrip, h.ItemID)
		return writeErr
	}); err != nil {
		t.Fatal(err)
	}
	f.want(f.anonymous, 200, "GET", gatePath("gate.assess-response", assessment(h)), nil, f.producer)
	variants := []struct {
		name   string
		change func(map[string]any)
	}{
		{"missing", func(r map[string]any) { delete(r, "native_commit") }},
		{"unobserved", func(r map[string]any) { delete(r["native_commit"].(map[string]any), "observed_at") }},
		{"wrong_record", func(r map[string]any) { r["native_commit"].(map[string]any)["binding_hash"] = "different" }},
		{"wrong_attempt", func(r map[string]any) { r["native_commit"].(map[string]any)["attempt"] = strings.Repeat("a", 32) }},
		{"wrong_protocol", func(r map[string]any) { r["native_commit"].(map[string]any)["version"] = "other" }},
		{"wrong_option", func(r map[string]any) {
			r["native_commit"].(map[string]any)["proposal"].(map[string]any)["option_id"] = "not-recorded"
		}},
		{"wrong_native_id", func(r map[string]any) {
			r["native_commit"].(map[string]any)["proposal"].(map[string]any)["native_request_id"] = "wrong"
		}},
		{"wrong_participant", func(r map[string]any) {
			r["native_commit"].(map[string]any)["proposal"].(map[string]any)["participant"] = map[string]any{"principal_ref": "user:other"}
		}},
		{"late", func(r map[string]any) {
			e := r["native_commit"].(map[string]any)
			e["observed_at"] = e["proposal"].(map[string]any)["expires_at"]
		}},
	}
	for _, v := range variants {
		t.Run(v.name, func(t *testing.T) {
			var fields map[string]any
			decoder := json.NewDecoder(bytes.NewReader(original))
			decoder.UseNumber()
			if err := decoder.Decode(&fields); err != nil {
				t.Fatal(err)
			}
			v.change(fields)
			changed, err := json.Marshal(fields)
			if err != nil {
				t.Fatal(err)
			}
			if err = f.inbox.Store.DB.Writer.Submit(context.Background(), "test_witness_corruption", func(ctx context.Context, tx store.Tx) error {
				_, writeErr := tx.ExecContext(ctx, "UPDATE approval_items SET record=? WHERE id=?", changed, h.ItemID)
				return writeErr
			}); err != nil {
				t.Fatal(err)
			}
			assertNativeQuarantine(t, f, h)
		})
	}
	if err := f.inbox.Store.DB.Writer.Submit(context.Background(), "test_restore_exact_witness", func(ctx context.Context, tx store.Tx) error {
		_, err := tx.ExecContext(ctx, "UPDATE approval_items SET record=? WHERE id=?", original, h.ItemID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	f.want(f.anonymous, 200, "GET", gatePath("gate.assess-response", assessment(h)), nil, f.producer)
}

func refuseNativeConfirmation(t *testing.T, f *nativeFixture) {
	t.Helper()
	ctx := context.Background()
	statements := []string{`CREATE TRIGGER test_refuse_native_confirmation BEFORE UPDATE OF record ON approval_items WHEN json_extract(CAST(NEW.record AS TEXT),'$.native_commit.observed_at') IS NOT NULL BEGIN SELECT RAISE(ABORT,'confirmation unavailable'); END`}
	if storetest.Postgres() {
		statements = []string{`CREATE FUNCTION test_refuse_native_confirmation() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF convert_from(NEW.record,'UTF8')::jsonb #> '{native_commit,observed_at}' IS NOT NULL THEN RAISE EXCEPTION 'confirmation unavailable'; END IF; RETURN NEW; END $$`, `CREATE TRIGGER test_refuse_native_confirmation BEFORE UPDATE ON approval_items FOR EACH ROW EXECUTE FUNCTION test_refuse_native_confirmation()`}
	}
	for _, q := range statements {
		if err := f.inbox.Store.DB.Writer.Submit(ctx, "test_confirmation_failure", func(ctx context.Context, tx store.Tx) error { _, err := tx.ExecContext(ctx, q); return err }); err != nil {
			t.Fatal(err)
		}
	}
}

func nativeOwnerConfig(t *testing.T, f *nativeFixture) libstore.Config {
	t.Helper()
	ctx := context.Background()
	var cfg libstore.Config
	if storetest.Postgres() {
		var schema string
		if err := f.inbox.Store.DB.Reader.QueryRowContext(ctx, "SELECT current_schema()").Scan(&schema); err != nil {
			t.Fatal(err)
		}
		dsn := os.Getenv(storetest.EnvDSN)
		if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
			parsed, parseErr := url.Parse(dsn)
			if parseErr != nil {
				t.Fatal("test DSN does not parse")
			}
			q := parsed.Query()
			q.Set("search_path", schema)
			parsed.RawQuery = q.Encode()
			dsn = parsed.String()
		} else {
			dsn += " search_path=" + schema
		}
		cfg = libstore.Config{Driver: libstore.DriverPostgres, Postgres: libstore.PostgresConfig{DSN: dsn, AllowInsecure: true}}
	} else {
		var seq int
		var name, path string
		if err := f.inbox.Store.DB.Reader.QueryRowContext(ctx, "PRAGMA database_list").Scan(&seq, &name, &path); err != nil {
			t.Fatal(err)
		}
		cfg = libstore.Config{Driver: libstore.DriverSQLite, SQLite: libstore.SQLiteConfig{Path: path}}
	}
	return cfg
}

// The explicit lane runs the ACTUAL prior executable, not a replacement boot
// helper, against a real upgraded database with an unconfirmed native winner.
func TestNativeGateOlderExecutableRefuses(t *testing.T) {
	binary := os.Getenv("TACHYON_NATIVE_PRIOR_BINARY")
	if binary == "" {
		t.Skip("explicit actual prior executable compatibility lane")
	}
	f := nativeStack(t)
	refuseNativeConfirmation(t, f)
	n, h := f.enqueue("old-reader-refusal")
	f.want(f.owner, 503, "POST", browserPath(h)+"/resolve", choice(h, n.Options[0].ID), "")
	cfg := nativeOwnerConfig(t, f)
	if err := f.stopOwner(); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"auto", "verify"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			db := map[string]any{"schema_mode": mode}
			if cfg.Driver == libstore.DriverPostgres {
				dsnFile := filepath.Join(dir, "db.dsn")
				if err := os.WriteFile(dsnFile, []byte(cfg.Postgres.DSN), 0600); err != nil {
					t.Fatal(err)
				}
				db["driver"] = "postgres"
				db["postgres"] = map[string]any{"dsn_file": dsnFile, "allow_insecure": true}
			} else {
				db["sqlite"] = map[string]any{"path": cfg.SQLite.Path}
			}
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			addr := listener.Addr().String()
			_ = listener.Close()
			override := map[string]any{"database": db, "httphost": map[string]any{"addr": addr}, "logging": map[string]any{"level": "error"}, "identity": map[string]any{"setup": "offline"}}
			raw, _ := json.Marshal(override)
			config := filepath.Join(dir, "override.json")
			if err = os.WriteFile(config, raw, 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, binary, "serve") //nolint:gosec // explicitly supplied frozen test executable, fixed serve argument
			command.Env = append(os.Environ(), "SUBSTRATE_CONFIG="+config, "TACHYON_DATA_DIR="+filepath.Join(dir, "data"))
			output, runErr := command.CombinedOutput()
			if runErr == nil || ctx.Err() != nil || !strings.Contains(string(output), "schema version mismatch") || !strings.Contains(string(output), "unknown to this build [5]") {
				t.Fatalf("prior %s startup did not fail on reader protocol: %v %s", mode, runErr, output)
			}
			conn, dialErr := net.DialTimeout("tcp", addr, 100*time.Millisecond)
			if dialErr == nil {
				_ = conn.Close()
				t.Fatal("old reader listener remained available")
			}
			t.Logf("prior executable %s startup refused applied reader protocol5 before serve", mode)
		})
	}
	// Refused startups must leave the sole unconfirmed outcome unavailable.
	reopened, err := store.Open(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if _, err = (&approvals.Store{DB: reopened}).Get(context.Background(), h.ItemID); !errors.Is(err, approvals.ErrNativeUnconfirmed) {
		t.Fatalf("old startup attempts changed quarantine: %v", err)
	}
}

func verifyNativeBackup(t *testing.T, f *nativeFixture, h hitl.Handle, confirmed bool) {
	t.Helper()
	if storetest.Postgres() {
		return
	} // PG snapshots are platform-owned; its real owner reopen lane covers bytea persistence.
	path := filepath.Join(t.TempDir(), "owner-backup.db")
	if result, err := f.inbox.Store.DB.Lib().Backup(context.Background(), path, libstore.BackupOptions{}); err != nil || !result.IntegrityOK {
		t.Fatalf("owner backup: %v", err)
	}
	copyDB, err := store.OpenFile(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer copyDB.Close()
	copyOwner := &approvals.Store{DB: copyDB}
	original, _ := retainedNativeEvidence(t, f.inbox.Store, h.ItemID)
	copied, _ := retainedNativeEvidence(t, copyOwner, h.ItemID)
	if !bytes.Equal(original, copied) {
		t.Fatal("backup changed private evidence or immutable choice bytes")
	}
	rec, readErr := copyOwner.Get(context.Background(), h.ItemID)
	if confirmed {
		if readErr != nil || rec.Outcome == nil {
			t.Fatalf("backup lost valid witness: %v", readErr)
		}
	} else if !errors.Is(readErr, approvals.ErrNativeUnconfirmed) {
		t.Fatalf("backup upgraded uncertain choice to authority: %v", readErr)
	}
}

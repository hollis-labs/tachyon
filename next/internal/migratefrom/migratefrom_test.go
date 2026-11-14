package migratefrom

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	libstore "github.com/hollis-labs/libs/core/store"
	"github.com/hollis-labs/libs/core/store/migrate"
	"github.com/hollis-labs/libs/core/util/scheduler/sqlstore"

	"github.com/hollis-labs/tachyon/next/internal/store"
)

// The synthetic source: the old schema with a few rows that exercise what the migration must keep.
// No live data is in the repository.
const (
	scopeJSON  = `{"provider":"cerberus","instance":"unix:/run/cerberus.sock","collection":"hosts","kind":"machine"}`
	objectSync = "object-sync.v1"
)

func scheduleID(scope string) string {
	sum := sha256.Sum256([]byte(scope))
	return "object-sync-" + hex.EncodeToString(sum[:16])
}

// initSQL is the schema migration without its goose annotations: the old schema as the old build
// stored it.
func initSQL(t *testing.T) string {
	t.Helper()
	body, err := fs.ReadFile(store.Migrations, store.Root+"/sqlite/00001_init.sql")
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func execAll(t *testing.T, db *sql.DB, stmts ...string) {
	t.Helper()
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("%v\n%.200s", err, s)
		}
	}
}

// fixture writes a source directory (tachyon.db and jobs.db in rollback-journal mode, as a closed
// backup copy has no -wal) and lets edit change either file first.
func fixture(t *testing.T, edit func(src, jobs *sql.DB)) string {
	t.Helper()
	dir := t.TempDir()
	src, err := sql.Open("sqlite", filepath.Join(dir, TachyonFile))
	if err != nil {
		t.Fatal(err)
	}
	src.SetMaxOpenConns(1)
	execAll(t, src, `PRAGMA journal_mode = DELETE`, initSQL(t))
	for _, n := range ledgerNames {
		execAll(t, src, `INSERT INTO tachyon_migrations(name) VALUES ('`+n+`')`)
	}
	execAll(t, src,
		`INSERT INTO app_settings(key, value) VALUES ('account.profile', '{"name":"x"}')`,
		// A BLOB in a TEXT-declared column, as existing chat_events rows may be.
		`INSERT INTO chat_events(thread_ref, type, body, at_ns) VALUES ('t1', 'turn', X'7b2261223a317d', 1), ('t1', 'turn', 'plain text', 2)`,
		// A deleted top row: the sequence stays above max(seq).
		`INSERT INTO planner_events(event_id, at_ms, theme_id, subject_ref, kind, text, refs_json, delta_json, arrow, weight, source, source_event_id)
		 VALUES ('e1', 1, 'th', 's', 'task', 'x', '[]', '{}', 'up', 0.1, 'torque', 's1'), ('e2', 2, 'th', 's', 'task', 'y', '[]', '{}', 'up', 2.5, 'torque', 's2')`,
		`DELETE FROM planner_events WHERE event_id = 'e2'`,
		// A foreign key: approval_defers references approval_items.
		`INSERT INTO approval_items(id, caller_scope, idempotency_key, state, revision, terminal, expires_ns, created_ns, record)
		 VALUES ('i1', 'scope', 'k1', 'pending', 1, 0, NULL, 1, X'00ff')`,
		`INSERT INTO approval_defers(item_id, until_ns, created_ns) VALUES ('i1', 5, 1)`,
		`INSERT INTO approval_decision_log(event_id, ts_ns, item_id, kind) VALUES ('d1', 1, 'i1', 'deferred')`,
		`INSERT INTO object_sync_state(source_id, scope_json) VALUES ('`+scheduleID(scopeJSON)+`', '`+scopeJSON+`')`,
	)
	jobs, err := sql.Open("sqlite", filepath.Join(dir, JobsFile))
	if err != nil {
		t.Fatal(err)
	}
	jobs.SetMaxOpenConns(1)
	execAll(t, jobs, `PRAGMA journal_mode = DELETE`)
	if err := sqlstore.Migrate(context.Background(), jobs); err != nil {
		t.Fatal(err)
	}
	execAll(t, jobs,
		`INSERT INTO gosched_schedules(id, last_run, next_run, enabled, job_type, payload, retry_json)
		 VALUES ('`+scheduleID(scopeJSON)+`', '2026-11-14T19:25:00.000000000Z', '2026-11-14T19:30:00.000000000Z', 1, '`+objectSync+`', NULL, '{"max_attempts":3}')`,
		`INSERT INTO gosched_schedule_options(schedule_id, options_json) VALUES ('`+scheduleID(scopeJSON)+`', '{"Misfire":"run_once"}')`,
	)
	if edit != nil {
		edit(src, jobs)
	}
	for _, db := range []*sql.DB{src, jobs} {
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// target is a new database as the database and scheduler modules create it: goose with Tachyon's
// schema, then the scheduler's tables.
func target(t *testing.T) *libstore.Store {
	t.Helper()
	cfg := libstore.Config{Driver: libstore.DriverSQLite, SQLite: libstore.SQLiteConfig{Path: filepath.Join(t.TempDir(), "tachyon.db")}}
	st, err := libstore.Open(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := migrate.UpStore(context.Background(), st, store.Source()); err != nil {
		t.Fatal(err)
	}
	if err := sqlstore.Migrate(context.Background(), st.DB()); err != nil {
		t.Fatal(err)
	}
	return st
}

// instance is the fixture scope's upstream address, configured as the cutover would.
const instance = "unix:/run/cerberus.sock"

func options(dir string) Options {
	return Options{Source: dir, JobTypes: []string{objectSync}, Instances: map[string]string{"cerberus": instance}}
}

func TestMigratesEveryRowAndProvesIt(t *testing.T) {
	dir := fixture(t, nil)
	st := target(t)
	o := options(dir)
	o.Report = filepath.Join(t.TempDir(), ReportFile)
	r, err := Run(context.Background(), st, o)
	if err != nil {
		t.Fatal(err)
	}
	if !r.OK || !r.SourcesStable || r.Ledger != 56 || len(r.Tables) != 96+5 {
		t.Fatalf("report: ok=%v stable=%v ledger=%d tables=%d", r.OK, r.SourcesStable, r.Ledger, len(r.Tables))
	}
	rows := map[string]int64{}
	for _, tr := range r.Tables {
		if !tr.Equal || tr.SourceChecksum != tr.TargetChecksum {
			t.Errorf("%s not equal", tr.Name)
		}
		rows[tr.Name] = tr.TargetRows
	}
	for table, want := range map[string]int64{"tachyon_migrations": 56, "chat_events": 2, "planner_events": 1, "approval_defers": 1, "gosched_schedules": 1, "gosched_schedule_options": 1} {
		if rows[table] != want {
			t.Errorf("%s: %d rows, want %d", table, rows[table], want)
		}
	}
	// The storage class survives the copy, and the high-water mark above a deleted row is kept.
	var typ string
	if e := st.Reader().QueryRowContext(context.Background(), `SELECT typeof(body) FROM chat_events WHERE at_ns = 1`).Scan(&typ); e != nil || typ != "blob" {
		t.Errorf("chat_events.body stored as %q, %v", typ, e)
	}
	seq := map[string]SequenceRow{}
	for _, s := range r.Sequence {
		seq[s.Name] = s
	}
	if s := seq["planner_events"]; s.Source != 2 || s.Target != 2 {
		t.Errorf("planner_events sequence %+v, want 2 on both sides", s)
	}
	if r.Target.Integrity != "ok" || r.Target.GooseVersion != 1 {
		t.Errorf("target %+v", r.Target)
	}
	if len(r.Schedules) != 1 || !r.Schedules[0].IDMatchesScope || r.Schedules[0].Scope != scopeJSON || !strings.HasPrefix(r.Schedules[0].Instance, "equal") {
		t.Errorf("schedules %+v", r.Schedules)
	}
	// The report is on disk, private, and says the same.
	b, err := os.ReadFile(o.Report)
	if err != nil {
		t.Fatal(err)
	}
	var onDisk Report
	if err := json.Unmarshal(b, &onDisk); err != nil || !onDisk.OK {
		t.Fatalf("report on disk: %v %+v", err, onDisk.Error)
	}
	if fi, _ := os.Stat(o.Report); fi.Mode().Perm() != 0o600 {
		t.Errorf("report mode %o", fi.Mode().Perm())
	}
	// Nothing was written beside the sources.
	entries, _ := os.ReadDir(dir)
	if len(entries) != 2 {
		t.Errorf("source directory now holds %d entries", len(entries))
	}
}

// Each refusal stops the run before anything is copied, and says why.
func TestRefusals(t *testing.T) {
	cases := map[string]struct {
		edit    func(src, jobs *sql.DB)
		setup   func(t *testing.T, dir string, st *libstore.Store, o *Options)
		want    string
		noCopy  bool
		noSetup bool
	}{
		"non-empty -wal beside a source": {
			setup: func(t *testing.T, dir string, _ *libstore.Store, _ *Options) {
				if err := os.WriteFile(filepath.Join(dir, TachyonFile+"-wal"), []byte("pages"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
			want: "non-empty -wal",
		},
		"the source is a refused data directory": {
			setup: func(_ *testing.T, dir string, _ *libstore.Store, o *Options) { o.Refused = []string{dir} },
			want:  "must never read",
		},
		"the target is not empty": {
			setup: func(t *testing.T, _ string, st *libstore.Store, _ *Options) {
				if _, err := st.ExecContext(context.Background(), `INSERT INTO app_settings(key, value) VALUES ('x', 'y')`); err != nil {
					t.Fatal(err)
				}
			},
			want: "table app_settings already holds 1 row(s)",
		},
		"an unknown table": {
			edit: func(src, _ *sql.DB) { _, _ = src.Exec(`CREATE TABLE extra (x TEXT)`) },
			want: "table extra: not in the reference",
		},
		"an unknown column": {
			edit: func(src, _ *sql.DB) { _, _ = src.Exec(`ALTER TABLE app_settings ADD COLUMN extra TEXT`) },
			want: "table app_settings: columns differ",
		},
		"an unknown index": {
			edit: func(src, _ *sql.DB) { _, _ = src.Exec(`CREATE INDEX extra_ix ON app_settings(value)`) },
			want: "index extra_ix: not in the reference",
		},
		"an unknown trigger": {
			edit: func(src, _ *sql.DB) {
				_, _ = src.Exec(`CREATE TRIGGER extra_tr AFTER INSERT ON app_settings BEGIN SELECT 1; END`)
			},
			want: "trigger extra_tr: not in the reference",
		},
		"a leftover with rows": {
			edit: func(src, _ *sql.DB) {
				_, _ = src.Exec(`CREATE TABLE bus_events_055 (x TEXT); INSERT INTO bus_events_055 VALUES ('row')`)
			},
			want: "rebuild leftover bus_events_055 holds 1 row(s)",
		},
		"a ledger missing a name": {
			edit: func(src, _ *sql.DB) {
				_, _ = src.Exec(`DELETE FROM tachyon_migrations WHERE name = '056_agents_control_relaunch.sql'`)
			},
			want: "tachyon_migrations holds 55 name(s)",
		},
		"a ledger with an unknown name": {
			edit: func(src, _ *sql.DB) { _, _ = src.Exec(`INSERT INTO tachyon_migrations(name) VALUES ('057_next.sql')`) },
			want: "tachyon_migrations holds 57 name(s)",
		},
		"a scheduler schema mismatch": {
			edit: func(_, jobs *sql.DB) { _, _ = jobs.Exec(`ALTER TABLE gosched_schedules ADD COLUMN extra TEXT`) },
			want: "the scheduler schema is not the one the pinned sqlstore creates",
		},
		"an unregistered job type": {
			edit: func(_, jobs *sql.DB) { _, _ = jobs.Exec(`UPDATE gosched_schedules SET job_type = 'other.v1'`) },
			want: `job type "other.v1" is not registered`,
		},
		"a schedule id that is not its scope's hash": {
			edit: func(src, _ *sql.DB) {
				_, _ = src.Exec(`UPDATE object_sync_state SET scope_json = replace(scope_json, 'hosts', 'other')`)
			},
			want: "the id is not the hash of its recorded scope",
		},
		"a scope instance unlike the configured upstream": {
			setup: func(_ *testing.T, _ string, _ *libstore.Store, o *Options) {
				o.Instances = map[string]string{"cerberus": "unix:/elsewhere/cerberus.sock"}
			},
			want: "is not the configured upstream",
		},
		"an upstream that is not configured": {
			setup: func(_ *testing.T, _ string, _ *libstore.Store, o *Options) { o.Instances = nil },
			want:  "upstream cerberus is not configured",
		},
		"a source with an integrity failure": {
			edit: func(src, _ *sql.DB) {
				_, _ = src.Exec(`PRAGMA foreign_keys = OFF; INSERT INTO approval_defers(item_id, until_ns, created_ns) VALUES ('missing', 1, 1)`)
			},
			want: "1 foreign key violation(s)",
		},
		"no source": {
			setup: func(_ *testing.T, _ string, _ *libstore.Store, o *Options) { o.Source = "" },
			want:  "--source is required",
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			dir := fixture(t, c.edit)
			st := target(t)
			o := options(dir)
			if c.setup != nil {
				c.setup(t, dir, st, &o)
			}
			r, err := Run(context.Background(), st, o)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want one containing %q", err, c.want)
			}
			if r.OK || len(r.Tables) != 0 {
				t.Fatalf("report says ok=%v with %d tables", r.OK, len(r.Tables))
			}
			if name != "the target is not empty" {
				if n, _ := count(context.Background(), st.Reader(), "approval_items"); n != 0 {
					t.Fatalf("rows were copied: approval_items holds %d", n)
				}
			}
		})
	}
}

// A rehearsal may run with the upstreams blank, only when it says so; the scope is then reported as
// not checked.
func TestUncheckedInstancesOnlyWhenAllowed(t *testing.T) {
	o := options(fixture(t, nil))
	o.Instances, o.AllowUnchecked = nil, true
	r, err := Run(context.Background(), target(t), o)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Schedules) != 1 || !strings.HasPrefix(r.Schedules[0].Instance, "not checked") || !r.AllowUnchecked {
		t.Fatalf("schedules %+v, allow_unchecked_instances %v", r.Schedules, r.AllowUnchecked)
	}
}

// An empty rebuild leftover is dropped and reported; the run succeeds.
func TestAnEmptyLeftoverIsDropped(t *testing.T) {
	dir := fixture(t, func(src, _ *sql.DB) { _, _ = src.Exec(`CREATE TABLE objects_next (x TEXT)`) })
	r, err := Run(context.Background(), target(t), options(dir))
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Dropped) != 1 || r.Dropped[0] != "objects_next" {
		t.Fatalf("dropped %v", r.Dropped)
	}
}

func TestTargetDirectoryMustBeNew(t *testing.T) {
	dir := t.TempDir()
	if err := CheckTargetDir(filepath.Join(dir, "missing")); err != nil {
		t.Errorf("a missing directory: %v", err)
	}
	if err := CheckTargetDir(dir); err != nil {
		t.Errorf("an empty directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tachyon.db"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := CheckTargetDir(dir); err == nil || !strings.Contains(err.Error(), "not empty") {
		t.Errorf("a non-empty directory: %v", err)
	}
}

// The checksum sees a changed value, and a value that changed only its storage class.
func TestChecksumSeesValueAndStorageClass(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(1)
	// v has no declared type, so it keeps whatever storage class it is given (a TEXT column would
	// turn the integer 7 back into '7').
	execAll(t, db, `CREATE TABLE t (id INTEGER PRIMARY KEY, v, r REAL, b BLOB)`,
		`INSERT INTO t VALUES (1, 'a', 0.1, X'00'), (2, '7', 1e300, NULL)`)
	sum := func() string {
		t.Helper()
		_, s, err := Checksum(context.Background(), db, "t")
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	base := sum()
	for name, change := range map[string][2]string{
		"a value":                       {`UPDATE t SET v = 'b' WHERE id = 1`, `UPDATE t SET v = 'a' WHERE id = 1`},
		"only the storage class (int)":  {`UPDATE t SET v = 7 WHERE id = 2`, `UPDATE t SET v = '7' WHERE id = 2`},
		"only the storage class (blob)": {`UPDATE t SET v = CAST('a' AS BLOB) WHERE id = 1`, `UPDATE t SET v = 'a' WHERE id = 1`},
		"a real in its 17th digit":      {`UPDATE t SET r = 0.10000000000000002 WHERE id = 1`, `UPDATE t SET r = 0.1 WHERE id = 1`},
		"NULL against an empty blob":    {`UPDATE t SET b = X'' WHERE id = 2`, `UPDATE t SET b = NULL WHERE id = 2`},
		"a row":                         {`DELETE FROM t WHERE id = 2`, `INSERT INTO t VALUES (2, '7', 1e300, NULL)`},
	} {
		execAll(t, db, change[0])
		if sum() == base {
			t.Errorf("%s changed and the checksum did not", name)
		}
		execAll(t, db, change[1])
		if sum() != base {
			t.Fatalf("%s: restoring did not restore the checksum", name)
		}
	}
	var typ string
	if err := db.QueryRow(`SELECT typeof(v) FROM t WHERE id = 2`).Scan(&typ); err != nil || typ != "text" {
		t.Fatalf("the test's own premise: v stored as %q", typ)
	}
}

// After the copy, a value changed in the target (only its storage class) fails verification.
func TestVerificationFailsOnAnAlteredTarget(t *testing.T) {
	dir := fixture(t, nil)
	st := target(t)
	ctx := context.Background()
	src, err := openSource(filepath.Join(dir, TachyonFile))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = src.Close() }()
	jobs, err := openSource(filepath.Join(dir, JobsFile))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = jobs.Close() }()
	if _, e := Run(ctx, st, options(dir)); e != nil {
		t.Fatal(e)
	}
	if _, e := st.ExecContext(ctx, `UPDATE chat_events SET body = CAST(body AS TEXT) WHERE at_ns = 1`); e != nil {
		t.Fatal(e)
	}
	var r Report
	err = verify(ctx, st, src, jobs, []string{"chat_events"}, nil, &r)
	if err == nil || !strings.Contains(err.Error(), "chat_events") {
		t.Fatalf("err = %v", err)
	}
}

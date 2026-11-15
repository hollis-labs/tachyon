package store

import (
	"context"
	"database/sql"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"

	libstore "github.com/hollis-labs/libs/core/store"

	"github.com/hollis-labs/libs/core/store/migrate"
	"github.com/hollis-labs/tachyon/next/internal/schema"
	_ "modernc.org/sqlite"
)

// oldTree is the old build's store package, in the same checkout (the repository root is three
// levels up from next/internal/store). Slice P1 deletes it; the snapshot fixture remains.
var oldTree = filepath.Join("..", "..", "..", "internal", "localstore")

// liveFixture is `sqlite3 <copy> .schema` of a backup copy of the running database at old migration
// 059 (2026-11-15): schema only, no rows. Refresh it when an old migration is carried.
const liveFixture = "testdata/schema-snapshot-059.sql"

// frameworkTables are created by the migration machinery, not by Tachyon's schema.
var frameworkTables = []string{"goose_db_version", "sqlite_sequence"}

// describe reads the schema of db, leaving out the framework tables.
func describe(t *testing.T, db *sql.DB) schema.Schema {
	t.Helper()
	s, err := schema.Describe(context.Background(), db, func(table string) bool { return slices.Contains(frameworkTables, table) })
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func openSQLite(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// migrated applies a migration source the way the database module does (goose through libs
// core/store) to a new file, and returns it open.
func migrated(t *testing.T, src migrate.Source) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "tachyon.db")
	cfg := libstore.Config{Driver: libstore.DriverSQLite, SQLite: libstore.SQLiteConfig{Path: path}}
	if err := migrate.Up(context.Background(), cfg, src); err != nil {
		t.Fatal(err)
	}
	return openSQLite(t, path)
}

// oldTreeDB builds a database the way the old build does, from the old tree's own source: the
// baseline DDL literal in store.go, the ledger literal in migrations.go, then every migration file in
// name order in one transaction with foreign keys off (internal/localstore/migrations.go).
func oldTreeDB(t *testing.T) *sql.DB {
	t.Helper()
	if _, err := os.Stat(oldTree); err != nil {
		t.Skipf("old tree not in this checkout (%v): after promotion the snapshot fixture is the reference", err)
	}
	baseline := literalContaining(t, filepath.Join(oldTree, "store.go"), "CREATE TABLE IF NOT EXISTS app_settings")
	ledger := literalContaining(t, filepath.Join(oldTree, "migrations.go"), "CREATE TABLE IF NOT EXISTS tachyon_migrations")
	files, err := filepath.Glob(filepath.Join(oldTree, "migrations", "*.sql"))
	if err != nil || len(files) == 0 {
		t.Fatalf("old migrations: %v (%d files)", err, len(files))
	}
	sort.Strings(files)
	db := openSQLite(t, filepath.Join(t.TempDir(), "old.db"))
	exec := func(q string) {
		t.Helper()
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("%v\n%.200s", err, q)
		}
	}
	exec(baseline)
	exec("PRAGMA foreign_keys=OFF")
	exec("BEGIN")
	exec(ledger)
	for _, f := range files {
		body, err := os.ReadFile(f) //nolint:gosec // the old tree's own migration files
		if err != nil {
			t.Fatal(err)
		}
		exec(string(body))
		if _, err := db.Exec(`INSERT INTO tachyon_migrations(name) VALUES(?)`, filepath.Base(f)); err != nil {
			t.Fatal(err)
		}
	}
	exec("COMMIT")
	return db
}

// literalContaining returns the one string literal in a Go file that contains marker.
func literalContaining(t *testing.T, file, marker string) string {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), file, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var found []string
	ast.Inspect(f, func(n ast.Node) bool {
		if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING && strings.Contains(lit.Value, marker) {
			s, err := strconv.Unquote(lit.Value)
			if err != nil {
				t.Fatal(err)
			}
			found = append(found, s)
		}
		return true
	})
	if len(found) != 1 {
		t.Fatalf("%s: %d string literals contain %q, want 1", file, len(found), marker)
	}
	return found[0]
}

// fixtureDB executes the live snapshot's schema dump.
func fixtureDB(t *testing.T) *sql.DB {
	t.Helper()
	body, err := os.ReadFile(liveFixture)
	if err != nil {
		t.Fatal(err)
	}
	var kept []string
	for _, line := range strings.Split(string(body), "\n") {
		if strings.HasPrefix(line, "CREATE TABLE sqlite_sequence(") {
			continue // created by SQLite itself with the first AUTOINCREMENT table
		}
		kept = append(kept, line)
	}
	db := openSQLite(t, filepath.Join(t.TempDir(), "fixture.db"))
	if _, err := db.Exec(strings.Join(kept, "\n")); err != nil {
		t.Fatal(err)
	}
	return db
}

func requireEqual(t *testing.T, ref string, want, got schema.Schema) {
	t.Helper()
	if len(want) < 280 {
		t.Fatalf("the %s reference has only %d objects: it was not built", ref, len(want))
	}
	if d := schema.Diff(want, got); len(d) > 0 {
		t.Fatalf("schema differs from %s in %d place(s):\n%s", ref, len(d), strings.Join(d, "\n"))
	}
}

// oldOnly is the migration source holding only the old schema's migrations (OldSchemaFiles): the old
// build's schema at its latest carried migration. The other migrations add Tachyon's own tables.
func oldOnly(t *testing.T) migrate.Source {
	t.Helper()
	files := fstest.MapFS{}
	for _, f := range OldSchemaFiles {
		body, err := fs.ReadFile(Migrations, Root+"/sqlite/"+f)
		if err != nil {
			t.Fatal(err)
		}
		files["migrations/sqlite/"+f] = &fstest.MapFile{Data: body}
	}
	return migrate.Source{FS: files, Root: Root}
}

// The old schema equals a database the old build's own source creates at this checkout (old migrations
// 001..059). When main brings
// a new old-tree migration onto the branch, this fails until the port carries it (plan section 1).
func TestSchemaEqualsTheOldTree(t *testing.T) {
	requireEqual(t, "the old tree", describe(t, oldTreeDB(t)), describe(t, migrated(t, oldOnly(t))))
}

// The schema equals the live database's schema (its backup copy of 2026-11-15, at 059).
func TestSchemaEqualsTheLiveSnapshot(t *testing.T) {
	requireEqual(t, "the live snapshot", describe(t, fixtureDB(t)), describe(t, migrated(t, oldOnly(t))))
}

// The comparison sees a one-word change: one column default altered on purpose.
func TestSchemaDiffSeesAChangedDefault(t *testing.T) {
	body, err := fs.ReadFile(Migrations, Root+"/sqlite/00001_init.sql")
	if err != nil {
		t.Fatal(err)
	}
	const from, to = `reason      TEXT NOT NULL DEFAULT ''`, `reason      TEXT NOT NULL DEFAULT 'x'`
	if strings.Count(string(body), from) != 1 {
		t.Fatalf("the probe text %q is not unique in the migration", from)
	}
	src := oldOnly(t)
	changed := src.FS.(fstest.MapFS)
	changed["migrations/sqlite/00001_init.sql"] = &fstest.MapFile{Data: []byte(strings.Replace(string(body), from, to, 1))}
	d := schema.Diff(describe(t, fixtureDB(t)), describe(t, migrated(t, src)))
	if len(d) != 2 {
		t.Fatalf("a changed default should be exactly two differences (columns and sql), got %d:\n%s", len(d), strings.Join(d, "\n"))
	}
	joined := strings.Join(d, "\n")
	if !strings.Contains(joined, "approval_defers") || !strings.Contains(joined, "'x'") {
		t.Fatalf("the diff does not name the change:\n%s", joined)
	}
}

// approval_decision_log is append-only: the two triggers refuse UPDATE and DELETE.
func TestDecisionLogIsAppendOnly(t *testing.T) {
	db := migrated(t, Source())
	for _, trig := range []string{"approval_decision_log_no_update", "approval_decision_log_no_delete"} {
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type = 'trigger' AND name = ? AND tbl_name = 'approval_decision_log'`, trig).Scan(&n); err != nil || n != 1 {
			t.Fatalf("trigger %s: %d, %v", trig, n, err)
		}
	}
	if _, err := db.Exec(`INSERT INTO approval_decision_log(event_id, ts_ns, item_id, kind) VALUES ('e1', 1, 'i1', 'resolved')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE approval_decision_log SET note = 'changed' WHERE event_id = 'e1'`); err == nil {
		t.Error("UPDATE succeeded")
	}
	if _, err := db.Exec(`DELETE FROM approval_decision_log WHERE event_id = 'e1'`); err == nil {
		t.Error("DELETE succeeded")
	}
	var note string
	if err := db.QueryRow(`SELECT note FROM approval_decision_log WHERE event_id = 'e1'`).Scan(&note); err != nil || note != "" {
		t.Fatalf("the row changed: %q, %v", note, err)
	}
}

// The shape facts the data migration (D1) relies on.
func TestShapeFacts(t *testing.T) {
	db := migrated(t, Source())
	var auto []string
	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type = 'table' AND sql LIKE '%AUTOINCREMENT%' AND name <> 'goose_db_version' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		auto = append(auto, n)
	}
	_ = rows.Close()
	want := []string{"agents_events", "agents_gaps", "agents_stream", "alert_events", "approval_decision_log", "bus_events",
		"card_event_outbox", "catalog_snapshots", "chat_activity", "chat_events", "checkpoint_events", "fileview_audit", "forge_audit", "planner_events", "planner_stream"}
	if !slices.Equal(auto, want) {
		t.Errorf("AUTOINCREMENT tables = %v, want %v", auto, want)
	}
	// chat_events.body keeps its declared type TEXT, although the live rows hold BLOBs: D1 copies the
	// storage class as it is.
	var typ string
	if err := db.QueryRow(`SELECT type FROM pragma_table_info('chat_events') WHERE name = 'body'`).Scan(&typ); err != nil || typ != "TEXT" {
		t.Errorf("chat_events.body declared %q, %v", typ, err)
	}
	var tables int
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name NOT IN ('goose_db_version', 'sqlite_sequence')`).Scan(&tables); err != nil || tables != 99+len(OwnTables) {
		t.Errorf("%d app tables, want 99 old ones (056, seat_status, ceremony_daily, checkpoint_events) plus Tachyon's own (%v)", tables, err)
	}
}

// InitSQL is the init migration without goose annotations, and the head version is the last file's.
func TestInitSQLAndHead(t *testing.T) {
	sqlText, err := InitSQL()
	if err != nil || strings.Contains(sqlText, "+goose") || !strings.Contains(sqlText, "CREATE TABLE approval_decision_log") {
		t.Fatalf("InitSQL: %v", err)
	}
	if v, err := HeadVersion(); err != nil || v != 4 {
		t.Fatalf("HeadVersion = %d, %v", v, err)
	}
}

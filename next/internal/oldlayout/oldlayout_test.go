package oldlayout

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func makeDB(t *testing.T, path, ddl string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(ddl); err != nil {
		t.Fatal(err)
	}
}

func TestNewOrEmptyDirectoriesPass(t *testing.T) {
	dir := t.TempDir()
	for _, p := range []string{"", ":memory:", filepath.Join(dir, "missing.db")} {
		if err := Refuse(p); err != nil {
			t.Errorf("Refuse(%q) = %v", p, err)
		}
	}
	empty := filepath.Join(dir, "empty.db")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Refuse(empty); err != nil {
		t.Errorf("empty file: %v", err)
	}
	fresh := filepath.Join(dir, "tachyon.db")
	makeDB(t, fresh, `CREATE TABLE goose_db_version (id INTEGER PRIMARY KEY)`)
	if err := Refuse(fresh); err != nil {
		t.Errorf("a new-layout database: %v", err)
	}
	// The new schema keeps the old ledger table (its rows are copied history): with the goose
	// version table beside it, it is the new layout.
	migrated := filepath.Join(dir, "migrated.db")
	makeDB(t, migrated, `CREATE TABLE goose_db_version (id INTEGER PRIMARY KEY); CREATE TABLE tachyon_migrations (name TEXT PRIMARY KEY, applied_at TEXT)`)
	if err := Refuse(migrated); err != nil {
		t.Errorf("a migrated new-layout database: %v", err)
	}
}

func TestTheOldLedgerIsRefused(t *testing.T) {
	db := filepath.Join(t.TempDir(), "tachyon.db")
	makeDB(t, db, `CREATE TABLE tachyon_migrations (name TEXT PRIMARY KEY, applied_at TEXT)`)
	before, err := os.ReadFile(db) //nolint:gosec // db is this test's temp file
	if err != nil {
		t.Fatal(err)
	}
	err = Refuse(db)
	if err == nil || !strings.Contains(err.Error(), "tachyon_migrations") || !strings.Contains(err.Error(), "migrate-from") {
		t.Fatalf("err = %v", err)
	}
	after, _ := os.ReadFile(db) //nolint:gosec // db is this test's temp file
	if string(before) != string(after) {
		t.Error("the check changed the old database")
	}
	if m, _ := filepath.Glob(db + "-*"); len(m) > 0 {
		t.Errorf("the check left files beside the database: %v", m)
	}
}

func TestJobsDBBesideTheDatabaseIsRefused(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "jobs.db"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Refuse(filepath.Join(dir, "tachyon.db")); err == nil || !strings.Contains(err.Error(), "jobs.db") {
		t.Fatalf("err = %v", err)
	}
}

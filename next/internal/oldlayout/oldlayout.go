// Package oldlayout refuses a data directory of the old (pre-Substrate) Tachyon build. The port is a
// clean break in code and a copy in data: the new build never opens, migrates or writes the old
// files. Their data reaches the new build only through `tachyon migrate-from` into a new, empty
// directory (port plan section 5, slice D1).
//
// The old layout is recognized by either sign, checked without writing anything:
//   - jobs.db beside the database (the old scheduler's file; the new build keeps schedules in its
//     one database), or
//   - a database holding the old migration ledger, table tachyon_migrations, without the goose
//     version table. The new schema keeps tachyon_migrations (its rows are copied history, port plan
//     section 5), so the ledger alone does not mark the old build; goose_db_version is what this
//     build adds and the old one never had.
package oldlayout

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite" // the "sqlite" driver, the one the database module uses
)

// Refuse returns an error naming dbPath when it, or its directory, is the old layout. An empty
// path or ":memory:" is not checked; a missing file is fine.
func Refuse(dbPath string) error {
	if dbPath == "" || dbPath == ":memory:" {
		return nil
	}
	dir := filepath.Dir(dbPath)
	if _, err := os.Stat(filepath.Join(dir, "jobs.db")); err == nil {
		return refusal(dir, "it holds jobs.db, the old build's scheduler file")
	}
	fi, err := os.Stat(dbPath)
	if errors.Is(err, os.ErrNotExist) || (err == nil && fi.Size() == 0) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("check %s: %w", dbPath, err)
	}
	old, err := oldLedger(dbPath)
	if err != nil {
		return fmt.Errorf("check %s for the old layout: %w", dbPath, err)
	}
	if old {
		return refusal(dir, "its database holds tachyon_migrations, the old build's schema ledger, and no goose version table")
	}
	return nil
}

// RefuseDir refuses dir when it is the old build's data directory: it holds jobs.db, or a
// tachyon.db with the old ledger. Every directory this build may create, tighten or write into is
// checked this way, not only the database's: the data directory, the signing key's and the cache's
// may each be pointed elsewhere by the override.
func RefuseDir(dir string) error {
	if dir == "" {
		return nil
	}
	return Refuse(filepath.Join(dir, "tachyon.db"))
}

func refusal(dir, why string) error {
	return fmt.Errorf("%s is a data directory of the old Tachyon build (%s); this build never opens it. "+
		"Point TACHYON_DATA_DIR (or database.sqlite.path) at a new, empty directory and copy the data with `tachyon migrate-from`", dir, why)
}

// oldLedger opens the file read-only and immutable (no journal, no lock, no write of any kind) and
// reports the old ledger table without the goose version table.
func oldLedger(path string) (bool, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return false, err
	}
	u := url.URL{Scheme: "file", Path: abs, RawQuery: "mode=ro&immutable=1"}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return false, err
	}
	defer func() { _ = db.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var ledger, goose int
	err = db.QueryRowContext(ctx, `SELECT
		count(*) FILTER (WHERE name = 'tachyon_migrations'),
		count(*) FILTER (WHERE name = 'goose_db_version')
		FROM sqlite_master WHERE type = 'table'`).Scan(&ledger, &goose)
	return ledger > 0 && goose == 0, err
}

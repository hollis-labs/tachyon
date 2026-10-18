package localstore

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
)

// These migrations extend the pre-existing cache/chat schema. Each migration
// and its ledger entry commit together before readers or the writer queue start.
//
//go:embed migrations/*.sql
var migrations embed.FS

func migrate(ctx context.Context, db *sql.DB) error {
	entries, err := migrations.ReadDir("migrations")
	if err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS tachyon_migrations (name TEXT PRIMARY KEY, applied_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')))`); err != nil {
		return err
	}
	for _, entry := range entries {
		var applied string
		err = tx.QueryRowContext(ctx, "SELECT name FROM tachyon_migrations WHERE name=?", entry.Name()).Scan(&applied)
		if err == nil {
			continue
		}
		if err != sql.ErrNoRows {
			return err
		}
		body, err := migrations.ReadFile("migrations/" + entry.Name())
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, string(body)); err != nil {
			return fmt.Errorf("migration %s: %w", entry.Name(), err)
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO tachyon_migrations(name) VALUES(?)", entry.Name()); err != nil {
			return err
		}
	}
	return tx.Commit()
}

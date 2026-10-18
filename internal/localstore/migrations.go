package localstore

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"time"
)

// These migrations extend the pre-existing cache/chat schema. Each migration
// and its ledger entry commit together before readers or the writer queue start.
//
//go:embed migrations/*.sql
var migrations embed.FS

func migrate(ctx context.Context, db *sql.DB) (err error) {
	entries, err := migrations.ReadDir("migrations")
	if err != nil {
		return err
	}
	// SQLite table rebuilds require disabling FK enforcement outside the
	// transaction. Pin the connection so the setting cannot escape into a pool;
	// the queue/readers are not started until migration and restoration succeed.
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err = conn.ExecContext(ctx, "PRAGMA foreign_keys=OFF"); err != nil {
		return err
	}
	defer func() {
		restoreCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, restoreErr := conn.ExecContext(restoreCtx, "PRAGMA foreign_keys=ON")
		err = errors.Join(err, restoreErr)
	}()
	tx, err := conn.BeginTx(ctx, nil)
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
	rows, err := tx.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return err
	}
	violations := rows.Next()
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if violations {
		return fmt.Errorf("migration foreign key validation failed")
	}
	return tx.Commit()
}

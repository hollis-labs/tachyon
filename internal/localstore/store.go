// Package localstore owns Tachyon's durable app database and single writer.
package localstore

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"

	"github.com/hollis-labs/libs/core/util/sqlite/serialwrite"
	"github.com/hollis-labs/libs/core/util/sqlite/sqlitekit"
)

type Store struct {
	Reader *sql.DB
	Writer *serialwrite.Queue
	db     *sql.DB
	done   chan error
}

func Open(ctx context.Context, path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	// Create privately before SQLite opens its WAL and shared-memory sidecars.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	err = f.Chmod(0600)
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		return nil, errors.Join(err, closeErr)
	}
	opts := sqlitekit.WriterOptions()
	opts.Synchronous = "FULL"
	db, err := sqlitekit.OpenWriter(ctx, path, sqlitekit.OpenOptions{Options: opts})
	if err != nil {
		return nil, err
	}
	_, err = db.ExecContext(ctx, `
 CREATE TABLE IF NOT EXISTS app_settings (key TEXT PRIMARY KEY, value TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS api_cache (
  key TEXT PRIMARY KEY, scope TEXT NOT NULL, identity TEXT NOT NULL,
  body BLOB NOT NULL, content_type TEXT NOT NULL, updated_at INTEGER NOT NULL
 );
 CREATE TABLE IF NOT EXISTS chat_outbox (
 identity TEXT NOT NULL, id TEXT NOT NULL, launch_id TEXT NOT NULL, body TEXT NOT NULL,
 created_at INTEGER NOT NULL, status TEXT NOT NULL, error TEXT NOT NULL, session_id TEXT NOT NULL,
 PRIMARY KEY(identity,id)
 );
 CREATE INDEX IF NOT EXISTS chat_outbox_launch ON chat_outbox(identity,launch_id,created_at);
 CREATE TABLE IF NOT EXISTS chat_turns (
 id TEXT PRIMARY KEY, source TEXT NOT NULL, channel TEXT NOT NULL, publication_id TEXT NOT NULL,
 launch_id TEXT NOT NULL, session_id TEXT NOT NULL, created_at TEXT NOT NULL,
 revision INTEGER NOT NULL, body BLOB NOT NULL, imported INTEGER NOT NULL DEFAULT 0
 );
 CREATE UNIQUE INDEX IF NOT EXISTS chat_publication ON chat_turns(source,channel,publication_id) WHERE publication_id<>'';
 CREATE TABLE IF NOT EXISTS chat_publications(source TEXT NOT NULL,channel TEXT NOT NULL,publication_id TEXT NOT NULL,sequence INTEGER NOT NULL,envelope BLOB NOT NULL,PRIMARY KEY(source,channel,publication_id));
 CREATE TABLE IF NOT EXISTS chat_turn_revisions(turn_id TEXT NOT NULL,revision INTEGER NOT NULL,body BLOB NOT NULL,recorded_at TEXT NOT NULL,PRIMARY KEY(turn_id,revision));
 CREATE TABLE IF NOT EXISTS chat_cursors(source TEXT NOT NULL,channel TEXT NOT NULL,cursor INTEGER NOT NULL,PRIMARY KEY(source,channel));
 CREATE TABLE IF NOT EXISTS chat_sync_status(source TEXT PRIMARY KEY,updated_at TEXT NOT NULL,problem TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS chat_reply_intents(turn_id TEXT PRIMARY KEY,request_key TEXT NOT NULL,body TEXT NOT NULL,reply_id TEXT NOT NULL DEFAULT '',state TEXT NOT NULL,error TEXT NOT NULL DEFAULT '',created_at TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS chat_imports(source TEXT PRIMARY KEY,checksum TEXT NOT NULL,imported_at TEXT NOT NULL,turn_count INTEGER NOT NULL, snapshot BLOB NOT NULL);
 CREATE INDEX IF NOT EXISTS api_cache_age ON api_cache(updated_at);
 `)
	if err != nil {
		db.Close()
		return nil, err
	}
	if err := migrate(ctx, db); err != nil {
		db.Close()
		return nil, err
	}
	reader, err := sqlitekit.OpenReader(ctx, path, sqlitekit.OpenOptions{})
	if err != nil {
		db.Close()
		return nil, err
	}
	s := &Store{Reader: reader, db: db, Writer: serialwrite.New(db, serialwrite.Options{}), done: make(chan error, 1)}
	// HTTP drains before Close. Do not cancel the worker with the signal context:
	// accepted operations must commit before the database closes.
	go func() { s.done <- s.Writer.Run(context.Background()) }()
	return s, nil
}

func (s *Store) Close() error {
	s.Writer.Stop()
	s.Writer.Wait()
	return errors.Join(<-s.done, s.Reader.Close(), s.db.Close())
}

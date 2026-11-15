package store

import (
	"context"

	libstore "github.com/hollis-labs/libs/core/store"
	"github.com/hollis-labs/libs/core/store/migrate"
)

// Tx is a write transaction: the database module's store.Tx. Use only the tx a WriteTx callback is
// given.
type Tx = libstore.Tx

// Store is the surface the ported domain packages use (the old build's localstore shape) over the
// database module's store: Reader is the read pool, Writer.Submit is WriteTx. The store rules hold:
// read on Reader, write in Submit using only its tx, never write while a cursor is open, and give a
// Submit callback no side effect but the tx (it is retried on contention: SQLITE_BUSY, or a Postgres serialization failure).
type Store struct {
	Reader libstore.Querier
	Writer Writer
	st     *libstore.Store
	owned  bool
}

// Writer runs write transactions.
type Writer struct{ st *libstore.Store }

// Submit runs fn in one write transaction. name labels the write in errors (the old queue's
// operation name); it has no other effect.
func (w Writer) Submit(ctx context.Context, _ string, fn func(context.Context, Tx) error) error {
	return WriteTx(ctx, w.st, fn)
}

// Wrap is the Store over the database module's store (database.From(app)).
func Wrap(st *libstore.Store) *Store {
	return &Store{Reader: st.Reader(), Writer: Writer{st: st}, st: st}
}

// OpenFile opens (and migrates) a database file on its own, for tests and offline tools that run
// without the module runtime. Close it when done.
func OpenFile(ctx context.Context, path string) (*Store, error) {
	return Open(ctx, libstore.Config{Driver: libstore.DriverSQLite, SQLite: libstore.SQLiteConfig{Path: path}})
}

// Open opens (and migrates) a database of either driver on its own, as OpenFile does for a file.
func Open(ctx context.Context, cfg libstore.Config) (*Store, error) {
	st, err := libstore.Open(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if err := migrate.UpStore(ctx, st, Source()); err != nil {
		_ = st.Close()
		return nil, err
	}
	s := Wrap(st)
	s.owned = true
	return s, nil
}

// Lib is the underlying store.
func (s *Store) Lib() *libstore.Store { return s.st }

// Close closes a store opened with OpenFile; a wrapped module store is the module's to close.
func (s *Store) Close() error {
	if !s.owned {
		return nil
	}
	return s.st.Close()
}

// ReadTx runs fn in one read-only transaction on the read pool (one consistent snapshot).
func (s *Store) ReadTx(ctx context.Context, fn func(context.Context, libstore.Querier) error) error {
	return s.st.ReadTx(ctx, fn)
}

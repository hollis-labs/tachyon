package store

import (
	"context"
	"fmt"

	libstore "github.com/hollis-labs/libs/core/store"
)

// The per-dialect SQL helpers (port plan section 14). Domain SQL is written once with ? placeholders
// (the libs store rebinds them for the driver) and portable constructs; the few constructs that
// differ between SQLite and Postgres are rendered here, and only here. docs/conversion-notes.md
// (Dialects) lists each construct, its helper, and the Postgres migration rule.

// Dialect is the store's SQL dialect.
func (s *Store) Dialect() libstore.Dialect { return s.st.Dialect() }

// RevisionSeq is the column that orders object_revisions rows by insertion, for a cursor over new
// revisions: SQLite's implicit rowid, and on Postgres the row_seq identity column that stands in for
// it (migrations/postgres/00001_init.sql). alias is the table's alias in the query.
//
// The values commit in order on both dialects because every write is serialized (WriteTx); without
// that, a Postgres sequence value taken by one writer could commit after a larger one, and a cursor
// would pass it.
func RevisionSeq(d libstore.Dialect, alias string) string {
	if d.Name() == libstore.DriverPostgres {
		return alias + ".row_seq"
	}
	return alias + ".rowid"
}

// JSONInt renders an integer read of a JSON column at path (keys), for either dialect. It panics on
// an invalid column or path: both are literals in the calling code.
func JSONInt(d libstore.Dialect, column string, path ...string) string {
	expr, err := d.JSONInt(column, path...)
	if err != nil {
		panic(fmt.Sprintf("store.JSONInt(%q, %q): %v", column, path, err))
	}
	return expr
}

// writeLock is the first key of the Postgres advisory lock every Tachyon write transaction holds;
// the second is the hash of the current schema. Advisory locks are per database, so the schema key
// keeps two Tachyon schemas in one database (or two tests) from serializing each other.
const writeLock int32 = 0x74616368 // "tach"

// WriteTx runs fn in one write transaction with Tachyon's writes serialized, on either dialect. The
// domain code reads and then writes inside one transaction (the next revision, an upsert by source
// key, a reorder), which is safe because SQLite has one writer. Postgres runs write transactions
// concurrently under read committed, so on Postgres the transaction first takes one advisory lock
// that every Tachyon write takes, held until commit: one writer at a time, as on SQLite. It also
// makes RevisionSeq values commit in order. Every write goes through here (Writer.Submit does).
func WriteTx(ctx context.Context, st *libstore.Store, fn func(context.Context, Tx) error) error {
	return st.WriteTx(ctx, func(ctx context.Context, tx Tx) error {
		if st.Dialect().Name() == libstore.DriverPostgres {
			if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(?, hashtext(current_schema()))`, writeLock); err != nil {
				return err
			}
		}
		return fn(ctx, tx)
	})
}

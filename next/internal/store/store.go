// Package store is Tachyon's schema and its access to the database module's store.
//
// The schema is one goose migration, migrations/sqlite/00001_init.sql: the old build's schema at migration
// 056 plus its baseline tables, byte for byte (port plan section 3). Register it with Use before
// boot; the database module applies it on start.
//
// Domain packages reach the database through From, and keep the store's rules (adopting-substrate
// section 4): reads on Reader(), writes in WriteTx using only the tx they are given, never a write
// while a cursor is open, and no side effects in a WriteTx callback (it is retried on SQLITE_BUSY).
package store

import (
	"embed"

	libstore "github.com/hollis-labs/libs/core/store"
	"github.com/hollis-labs/libs/core/store/migrate"
	"github.com/hollis-labs/substrate/module"
	"github.com/hollis-labs/substrate/modules/database"
)

// Root is the directory of the migration files inside Migrations.
const Root = "migrations"

// Migrations are the embedded goose migrations.
//
//go:embed migrations/sqlite/*.sql
var Migrations embed.FS

// Source is the migration source, for the database module and for tests.
func Source() migrate.Source { return migrate.Source{FS: Migrations, Root: Root} }

// Use registers the schema with the database module. Call it before boot.
func Use() { database.UseMigrationFS(Migrations, Root) }

// From returns the database module's store, or nil when the module is not started.
func From(app *module.App) *libstore.Store { return database.From(app) }

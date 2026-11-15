// Package storetest gives a test its own database on the dialect the suite runs on (port plan
// section 14, slice PG1). With SUBSTRATE_TEST_POSTGRES_DSN unset the suite runs on SQLite: each
// database is a file in the test's temp directory. With it set the suite runs on Postgres: each
// database is a fresh schema on that server, dropped when the test ends. CI runs the suite once
// each way.
package storetest

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"

	libstore "github.com/hollis-labs/libs/core/store"

	"github.com/hollis-labs/tachyon/next/internal/store"
)

// Env names read by the package.
const (
	// EnvDSN is the Postgres server the suite runs on. It is a secret: never print it.
	EnvDSN = "SUBSTRATE_TEST_POSTGRES_DSN"
	// EnvRequire=1 fails, instead of passing on SQLite, a run that was meant to be on Postgres.
	EnvRequire = "SUBSTRATE_REQUIRE_POSTGRES"
)

// Postgres reports whether the suite runs on Postgres.
func Postgres() bool { return os.Getenv(EnvDSN) != "" }

// Driver is the driver the suite runs on.
func Driver() libstore.Driver {
	if Postgres() {
		return libstore.DriverPostgres
	}
	return libstore.DriverSQLite
}

// RequirePostgres skips a test that only means something on Postgres when the suite runs on SQLite,
// or fails it when EnvRequire=1.
func RequirePostgres(t *testing.T) {
	t.Helper()
	if Postgres() {
		return
	}
	if os.Getenv(EnvRequire) == "1" {
		t.Fatalf("%s=1 but %s is not set", EnvRequire, EnvDSN)
	}
	t.Skipf("%s is not set; this test runs in the Postgres pass", EnvDSN)
}

// SkipOnPostgres skips a test that only applies to SQLite, with the reason.
func SkipOnPostgres(t *testing.T, reason string) {
	t.Helper()
	if Postgres() {
		t.Skip("SQLite only: " + reason)
	}
}

// Database is one test's database.
type Database struct {
	// Config opens it with the libs store.
	Config libstore.Config
	// dsnFile holds the Postgres connection string (0600, in the test's temp directory), for the
	// database module's dsn_file.
	dsnFile string
}

// New makes an empty database for t on the suite's dialect.
func New(t *testing.T) Database {
	t.Helper()
	return NewAt(t, filepath.Join(t.TempDir(), "tachyon.db"))
}

// NewAt is New with the SQLite file at path (a test that also needs the file's directory). On
// Postgres the path is not used.
func NewAt(t *testing.T, path string) Database {
	t.Helper()
	if !Postgres() {
		return Database{Config: libstore.Config{Driver: libstore.DriverSQLite, SQLite: libstore.SQLiteConfig{Path: path}}}
	}
	dsn := freshSchema(t)
	file := filepath.Join(t.TempDir(), "pg.dsn")
	if err := os.WriteFile(file, []byte(dsn), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := libstore.Config{Driver: libstore.DriverPostgres, Postgres: libstore.PostgresConfig{DSN: dsn, AllowInsecure: true, MaxConns: 8}}
	return Database{Config: cfg, dsnFile: file}
}

// Open opens and migrates the database; it is closed when the test ends. Opening it again gives a
// second handle on the same data, as reopening a file does.
func (d Database) Open(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(context.Background(), d.Config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// ModuleConfig is the database module's config for this database. On Postgres the test migrates its
// own schema (schema_mode auto); a deployment's mode and roles are slice PG2's.
func (d Database) ModuleConfig() map[string]any {
	if d.Config.Driver == libstore.DriverPostgres {
		return map[string]any{"driver": "postgres", "schema_mode": "auto", "postgres": map[string]any{"dsn_file": d.dsnFile, "allow_insecure": true}}
	}
	return map[string]any{"sqlite": map[string]any{"path": d.Config.SQLite.Path}}
}

// OverrideYAML is ModuleConfig as an override-file section (for the boot tests), or "" on SQLite,
// where the manifest's database path is used.
func (d Database) OverrideYAML() string {
	if d.Config.Driver != libstore.DriverPostgres {
		return ""
	}
	return "database:\n  driver: postgres\n  schema_mode: auto\n  postgres:\n    dsn_file: " + d.dsnFile + "\n    allow_insecure: true\n"
}

// freshSchema creates a schema of its own on the test server and returns a connection string that
// works in it. The schema is dropped when the test ends.
func freshSchema(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv(EnvDSN)
	cc, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("%s does not parse", EnvDSN) // never the value
	}
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	schema := "tachyon_t_" + hex.EncodeToString(b)
	admin := stdlib.OpenDB(*cc)
	defer func() { _ = admin.Close() }()
	exec(t, admin, "CREATE SCHEMA "+schema)
	t.Cleanup(func() {
		a := stdlib.OpenDB(*cc)
		defer func() { _ = a.Close() }()
		if _, err := a.ExecContext(context.Background(), "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Errorf("drop test schema %s: %v", schema, err)
		}
	})
	return withSearchPath(dsn, schema)
}

func withSearchPath(dsn, schema string) string {
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		sep := "?"
		if strings.Contains(dsn, "?") {
			sep = "&"
		}
		return dsn + sep + "search_path=" + schema
	}
	return dsn + " search_path=" + schema
}

func exec(t *testing.T, db *sql.DB, q string) {
	t.Helper()
	if _, err := db.ExecContext(context.Background(), q); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

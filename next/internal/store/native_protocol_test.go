package store_test

import (
	"context"
	"errors"
	"io/fs"
	"testing"
	"testing/fstest"

	libstore "github.com/hollis-labs/libs/core/store"
	"github.com/hollis-labs/libs/core/store/migrate"
	"github.com/hollis-labs/tachyon/next/internal/store"
	"github.com/hollis-labs/tachyon/next/internal/store/storetest"
)

// Exercise the actual pinned module startup primitive against a real migrated
// database. A reader from before confirmation must fail in auto AND verify mode.
func TestNativeProtocolRejectsOlderModuleStartup(t *testing.T) {
	fixture := storetest.New(t)
	db := fixture.Open(t)
	old := fstest.MapFS{}
	for _, dialect := range []string{"sqlite", "postgres"} {
		for _, name := range []string{"00001_init.sql", "00002_service_credentials.sql", "00003_old_057_058.sql", "00004_old_059.sql"} {
			path := "migrations/" + dialect + "/" + name
			raw, err := fs.ReadFile(store.Migrations, path)
			if err != nil {
				t.Fatal(err)
			}
			old[path] = &fstest.MapFile{Data: raw}
		}
	}
	ctx := context.Background()
	for _, mode := range []libstore.SchemaMode{libstore.SchemaAuto, libstore.SchemaVerify} {
		cfg := fixture.Config
		cfg.SchemaMode = mode
		if err := migrate.Ensure(ctx, cfg, db.Lib(), migrate.Source{FS: old, Root: "migrations"}); !errors.Is(err, migrate.ErrSchemaMismatch) {
			t.Fatalf("old startup mode %s did not refuse newer reader protocol: %v", mode, err)
		}
	}
	if err := migrate.Verify(ctx, db.Lib(), store.Source()); err != nil {
		t.Fatalf("old startup attempts changed the ledger: %v", err)
	}
}

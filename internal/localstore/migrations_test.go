package localstore

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/libs/core/util/sqlite/sqlitekit"
)

func TestUpgradePreservesExistingAppData(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "legacy.db")
	legacy, err := sqlitekit.OpenWriter(ctx, path, sqlitekit.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	// An existing installation predates the migration ledger and object tables.
	_, err = legacy.Exec(`CREATE TABLE app_settings(key TEXT PRIMARY KEY,value TEXT NOT NULL);
 INSERT INTO app_settings VALUES('existing-setting','keep');
 CREATE TABLE api_cache(key TEXT PRIMARY KEY,scope TEXT NOT NULL,identity TEXT NOT NULL,body BLOB NOT NULL,content_type TEXT NOT NULL,updated_at INTEGER NOT NULL);
 INSERT INTO api_cache VALUES('snapshot','forge','existing-identity','{"items":[]}','application/json',1)`)
	if err != nil {
		t.Fatal(err)
	}
	if err = legacy.Close(); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		upgraded, err := Open(ctx, path)
		if err != nil {
			t.Fatal(err)
		}
		var setting, body string
		if err = upgraded.Reader.QueryRow("SELECT value FROM app_settings WHERE key='existing-setting'").Scan(&setting); err != nil || setting != "keep" {
			t.Fatalf("setting lost: %v", err)
		}
		if err = upgraded.Reader.QueryRow("SELECT body FROM api_cache WHERE key='snapshot'").Scan(&body); err != nil || body != `{"items":[]}` {
			t.Fatalf("snapshot lost: %v", err)
		}
		if err = upgraded.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

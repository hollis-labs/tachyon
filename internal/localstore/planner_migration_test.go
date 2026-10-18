package localstore

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/libs/core/util/sqlite/sqlitekit"
)

// Seed the previous released schema with real FK relationships, not just an
// empty database. Both successful table replacement and rollback must preserve it.
func previousObjectStore(t *testing.T) (*sql.DB, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "previous.db")
	db, err := sqlitekit.OpenWriter(context.Background(), path, sqlitekit.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err = db.Exec(`CREATE TABLE tachyon_migrations(name TEXT PRIMARY KEY,applied_at TEXT NOT NULL DEFAULT 'test')`); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"001_objects.sql", "002_object_sync.sql"} {
		body, err := migrations.ReadFile("migrations/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec(string(body)); err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec("INSERT INTO tachyon_migrations(name) VALUES(?)", name); err != nil {
			t.Fatal(err)
		}
	}
	_, err = db.Exec(`
 INSERT INTO objects VALUES('project','project','source','Fabric',2,'{}','{"favorite":true}',NULL,'2026-10-18T00:00:00.000000000Z','2026-10-18T00:00:00.000000000Z');
 INSERT INTO objects VALUES('agent','agent','tachyon','Builder',1,'{"name":"Builder","enabled":true,"project_id":"project"}','{}',NULL,'2026-10-18T00:00:00.000000000Z','2026-10-18T00:00:00.000000000Z');
 INSERT INTO object_sources VALUES('project','tether','hub','registry/projects','project','urn:project','v1','2026-10-18T00:00:00.000000000Z',NULL,'{"name":"Fabric"}');
 INSERT INTO object_revisions VALUES('project',1,'observe','test','2026-10-18T00:00:00.000000000Z','{"id":"project","revision":1}');
 INSERT INTO object_revisions VALUES('project',2,'patch','test','2026-10-18T00:00:00.000000000Z','{"id":"project","revision":2}');
 INSERT INTO object_links VALUES('agent','project','','project');
 INSERT INTO object_sync_watermarks VALUES('tether','hub','registry/projects','project','2026-10-18T00:00:00.000000000Z');
 `)
	if err != nil {
		t.Fatal(err)
	}
	return db, path
}

func assertOldObjectData(t *testing.T, db *sql.DB) {
	t.Helper()
	var metadata, snapshot, target string
	var revision int
	if err := db.QueryRow("SELECT metadata_json,revision FROM objects WHERE id='project'").Scan(&metadata, &revision); err != nil || metadata != `{"favorite":true}` || revision != 2 {
		t.Fatalf("lost object: %s %d %v", metadata, revision, err)
	}
	if err := db.QueryRow("SELECT snapshot_json FROM object_sources WHERE object_id='project'").Scan(&snapshot); err != nil || snapshot != `{"name":"Fabric"}` {
		t.Fatal("lost source", err)
	}
	if err := db.QueryRow("SELECT to_id FROM object_links WHERE from_id='agent'").Scan(&target); err != nil || target != "project" {
		t.Fatal("lost link", err)
	}
	var historical string
	if err := db.QueryRow("SELECT record_json FROM object_revisions WHERE object_id='project' AND revision=1").Scan(&historical); err != nil || historical != `{"id":"project","revision":1}` {
		t.Fatal("lost revision", err)
	}
}

func TestPlannerMigrationPreservesObjectsAndForeignKeys(t *testing.T) {
	db, path := previousObjectStore(t)
	if err := migrate(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	assertOldObjectData(t, db)
	var foreignKeys int
	if err := db.QueryRow("PRAGMA foreign_keys").Scan(&foreignKeys); err != nil || foreignKeys != 1 {
		t.Fatal("FK enforcement not restored", err)
	}
	if _, err := db.Exec(`INSERT INTO object_links VALUES('missing','project','','project')`); err == nil {
		t.Fatal("FK violation accepted after rebuild")
	}
	if _, err := db.Exec(`INSERT INTO objects VALUES('task','task','tachyon','New task',1,'{"name":"New task","status":"todo","priority":0}','{}',NULL,'2026-10-18T00:00:00.000000000Z','2026-10-18T00:00:00.000000000Z')`); err != nil {
		t.Fatal("new kind rejected", err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	assertOldObjectData(t, reopened.Reader)
	rows, err := reopened.Reader.Query("PRAGMA foreign_key_check")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() || rows.Err() != nil {
		t.Fatal("migration left dangling FKs", rows.Err())
	}
}

func TestPlannerMigrationRollsBackOnForeignKeyViolation(t *testing.T) {
	db, _ := previousObjectStore(t)
	if _, err := db.Exec(`PRAGMA foreign_keys=OFF; INSERT INTO object_links VALUES('missing','project','','project'); PRAGMA foreign_keys=ON;`); err != nil {
		t.Fatal(err)
	}
	if err := migrate(context.Background(), db); err == nil {
		t.Fatal("accepted preexisting corruption")
	}
	assertOldObjectData(t, db)
	var applied string
	if err := db.QueryRow("SELECT name FROM tachyon_migrations WHERE name='003_planner.sql'").Scan(&applied); err != sql.ErrNoRows {
		t.Fatal("failed migration was recorded", err)
	}
	if _, err := db.Exec(`INSERT INTO objects VALUES('task','task','tachyon','New task',1,'{}','{}',NULL,'now','now')`); err == nil {
		t.Fatal("failed migration did not roll back kind constraint")
	}
	if _, err := db.Exec(`INSERT INTO object_links VALUES('another-missing','project','','project')`); err == nil {
		t.Fatal("FKs not restored after rollback")
	}
	if _, err := db.Exec(`DELETE FROM object_links WHERE from_id='missing'`); err != nil {
		t.Fatal(err)
	}
	if err := migrate(context.Background(), db); err != nil {
		t.Fatal("repaired database could not migrate", err)
	}
}

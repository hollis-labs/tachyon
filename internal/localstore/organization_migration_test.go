package localstore

import (
	"context"
	"database/sql"
	"testing"
)

func TestOrganizationMigrationPreservesPlannerProjection(t *testing.T) {
	db, path := previousObjectStore(t)
	body, err := migrations.ReadFile("migrations/003_planner.sql")
	if err != nil {
		t.Fatal(err)
	}
	// Reproduce the released 003 schema before applying the new migration.
	if _, err = db.Exec("PRAGMA foreign_keys=OFF;" + string(body) + ";PRAGMA foreign_keys=ON;"); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO tachyon_migrations(name) VALUES('003_planner.sql');
INSERT INTO objects VALUES('plan','plan','tachyon','Plan',1,'{"name":"Plan","status":"draft","priority":0,"project_id":"project"}','{}',NULL,'2026-10-18T00:00:00.000000000Z','2026-10-18T00:00:00.000000000Z');
INSERT INTO objects VALUES('task','task','tachyon','Task',1,'{"name":"Task","status":"todo","priority":2,"plan_id":"plan"}','{}',NULL,'2026-10-18T00:00:00.000000000Z','2026-10-18T00:00:00.000000000Z');
INSERT INTO planner_items VALUES('plan','plan','draft',0,'project','','','');
INSERT INTO planner_items VALUES('task','task','todo',2,'','plan','','');
INSERT INTO object_links VALUES('plan','project','','project');
INSERT INTO object_links VALUES('task','plan','','plan');
INSERT INTO object_revisions VALUES('task',1,'create','test','2026-10-18T00:00:00.000000000Z','{"id":"task","revision":1}');`); err != nil {
		t.Fatal(err)
	}
	if err = migrate(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	assertOldObjectData(t, db)
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	var kind, status, plan, epic, sprint, workstream, starts, ends string
	var priority int
	if err = reopened.Reader.QueryRow(`SELECT kind,status,priority,plan_id,epic_id,sprint_id,workstream_id,starts_at,ends_at FROM planner_items WHERE object_id='task'`).Scan(&kind, &status, &priority, &plan, &epic, &sprint, &workstream, &starts, &ends); err != nil || kind != "task" || status != "todo" || priority != 2 || plan != "plan" || epic != "" || sprint != "" || workstream != "" || starts != "" || ends != "" {
		t.Fatal("planner projection changed", err)
	}
	var target, history string
	if err = reopened.Reader.QueryRow(`SELECT to_id FROM object_links WHERE from_id='task'`).Scan(&target); err != nil || target != "plan" {
		t.Fatal("lost planning link", err)
	}
	if err = reopened.Reader.QueryRow(`SELECT record_json FROM object_revisions WHERE object_id='task'`).Scan(&history); err != nil || history != `{"id":"task","revision":1}` {
		t.Fatal("lost planning history", err)
	}
	var table string
	var rowid sql.NullInt64
	var parent string
	var fkid int
	if err = reopened.Reader.QueryRow("PRAGMA foreign_key_check").Scan(&table, &rowid, &parent, &fkid); err != sql.ErrNoRows {
		t.Fatal("dangling FK", err)
	}
}

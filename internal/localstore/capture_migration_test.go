package localstore

import (
	"context"
	"database/sql"
	"testing"
)

func TestCaptureMigrationPreservesOrganizationAndExecution(t *testing.T) {
	db, path := previousObjectStore(t)
	for _, name := range []string{"003_planner.sql", "004_planner_organization.sql", "005_execution_identities.sql"} {
		body, err := migrations.ReadFile("migrations/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec("PRAGMA foreign_keys=OFF;" + string(body) + ";PRAGMA foreign_keys=ON;"); err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec("INSERT INTO tachyon_migrations(name) VALUES(?)", name); err != nil {
			t.Fatal(err)
		}
	}
	_, err := db.Exec(`
 INSERT INTO objects VALUES('task','task','tachyon','Retained task',2,'{"name":"Retained task","status":"todo","priority":2}','{}','2026-10-18T01:00:00.000000000Z','2026-10-18T00:00:00.000000000Z','2026-10-18T01:00:00.000000000Z');
 INSERT INTO planner_items VALUES('task','task','todo',2,'project','plan','parent','due','epic','sprint','workstream','starts','ends');
 INSERT INTO objects VALUES('session','session','tachyon','Session',1,'{"name":"Session","status":"active"}','{}',NULL,'2026-10-18T00:00:00.000000000Z','2026-10-18T00:00:00.000000000Z');
 INSERT INTO execution_items VALUES('session','session','active','project','','agent','task','','','started','');
 INSERT INTO object_links VALUES('session','task','','task');
 INSERT INTO object_revisions VALUES('task',2,'delete','test','2026-10-18T01:00:00.000000000Z','{"id":"task","revision":2}');
 `)
	if err != nil {
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
	var projection string
	err = reopened.Reader.QueryRow(`SELECT kind||'|'||status||'|'||priority||'|'||project_id||'|'||plan_id||'|'||parent_id||'|'||due_at||'|'||epic_id||'|'||sprint_id||'|'||workstream_id||'|'||starts_at||'|'||ends_at FROM planner_items WHERE object_id='task'`).Scan(&projection)
	if err != nil || projection != "task|todo|2|project|plan|parent|due|epic|sprint|workstream|starts|ends" {
		t.Fatal("changed planner projection", projection, err)
	}
	var target, deleted, history string
	if err = reopened.Reader.QueryRow(`SELECT task_id FROM execution_items WHERE object_id='session'`).Scan(&target); err != nil || target != "task" {
		t.Fatal("execution lost", err)
	}
	if err = reopened.Reader.QueryRow(`SELECT deleted_at FROM objects WHERE id='task'`).Scan(&deleted); err != nil || deleted != "2026-10-18T01:00:00.000000000Z" {
		t.Fatal("tombstone lost", err)
	}
	if err = reopened.Reader.QueryRow(`SELECT record_json FROM object_revisions WHERE object_id='task'`).Scan(&history); err != nil || history != `{"id":"task","revision":2}` {
		t.Fatal("history lost", err)
	}
	var table, parent string
	var rowid sql.NullInt64
	var fkid int
	if err = reopened.Reader.QueryRow("PRAGMA foreign_key_check").Scan(&table, &rowid, &parent, &fkid); err != sql.ErrNoRows {
		t.Fatal("dangling reference", err)
	}
}

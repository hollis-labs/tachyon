package localstore

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/libs/core/util/sqlite/sqlitekit"
)

// 022 is additive: existing app-agent messages keep their rows and read as kind 'text'.
func TestChatPlansMigrationIsAdditive(t *testing.T) {
	db, err := sqlitekit.OpenWriter(context.Background(), filepath.Join(t.TempDir(), "previous.db"), sqlitekit.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`CREATE TABLE tachyon_migrations(name TEXT PRIMARY KEY,applied_at TEXT NOT NULL DEFAULT 'test')`); err != nil {
		t.Fatal(err)
	}
	entries, _ := migrations.ReadDir("migrations")
	for _, e := range entries {
		if e.Name() >= "022" {
			break
		}
		body, err := migrations.ReadFile("migrations/" + e.Name())
		if err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec(string(body)); err != nil {
			t.Fatal(e.Name(), err)
		}
		if _, err = db.Exec("INSERT INTO tachyon_migrations(name) VALUES(?)", e.Name()); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = db.Exec(`
 INSERT INTO app_agent_threads(id,agent,title,created_ns,updated_ns) VALUES('app-tachyon','tachyon','Tachyon',10,20);
 INSERT INTO app_agent_messages(id,thread_id,seq,role,body,state,created_ns,updated_ns) VALUES('m1','app-tachyon',1,'agent','hi','complete',10,10)`); err != nil {
		t.Fatal(err)
	}
	if err = migrate(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	var body, kind string
	var meta *string
	if err = db.QueryRow(`SELECT body,kind,meta_json FROM app_agent_messages WHERE id='m1'`).Scan(&body, &kind, &meta); err != nil || body != "hi" || kind != "text" || meta != nil {
		t.Fatal("message changed", body, kind, meta, err)
	}
	if _, err = db.Exec(`INSERT INTO app_agent_messages(id,thread_id,seq,role,body,state,created_ns,updated_ns,kind) VALUES('m2','app-tachyon',2,'agent','','complete',1,1,'bogus')`); err == nil {
		t.Fatal("kind CHECK not enforced")
	}
	// One plan per thread and per project.
	ins := `INSERT INTO chat_plans(id,scope,thread_ref,project_id,status,revision,created_by,created_ns,updated_ns) VALUES(?,?,?,?,'proposed',1,'user',1,1)`
	if _, err = db.Exec(ins, "a", "thread", "app:1", nil); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ins, "b", "thread", "app:1", nil); err == nil {
		t.Fatal("two plans for one thread")
	}
	if _, err = db.Exec(ins, "c", "thread", "app:2", "PRJ"); err == nil {
		t.Fatal("scope/owner CHECK not enforced")
	}
	if err = migrate(context.Background(), db); err != nil {
		t.Fatal(err)
	}
}

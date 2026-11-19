package localstore

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/libs/core/util/sqlite/sqlitekit"
)

// 019 is additive: a database already at 018 keeps its app-agent rows untouched and gains
// the chat thread tables.
func TestChatThreadsMigrationIsAdditive(t *testing.T) {
	db, err := sqlitekit.OpenWriter(context.Background(), filepath.Join(t.TempDir(), "previous.db"), sqlitekit.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`CREATE TABLE tachyon_migrations(name TEXT PRIMARY KEY,applied_at TEXT NOT NULL DEFAULT 'test')`); err != nil {
		t.Fatal(err)
	}
	entries, err := migrations.ReadDir("migrations")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() >= "019" {
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
 INSERT INTO app_agent_threads(id,agent,title,read_seq,created_ns,updated_ns) VALUES('app-tachyon','tachyon','Tachyon',1,10,20);
 INSERT INTO app_agent_messages(id,thread_id,seq,role,body,state,created_ns,updated_ns) VALUES('m1','app-tachyon',1,'agent','hi','complete',10,10)`); err != nil {
		t.Fatal(err)
	}
	if err = migrate(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	var title, body string
	if err = db.QueryRow(`SELECT t.title,m.body FROM app_agent_threads t JOIN app_agent_messages m ON m.thread_id=t.id`).Scan(&title, &body); err != nil || title != "Tachyon" || body != "hi" {
		t.Fatal("app agent data changed", title, body, err)
	}
	for _, table := range []string{"chat_thread_state", "chat_events", "chat_message_keys"} {
		if _, err = db.Exec("SELECT COUNT(*) FROM " + table); err != nil {
			t.Fatal(table, err)
		}
	}
	// Running again is a no-op.
	if err = migrate(context.Background(), db); err != nil {
		t.Fatal(err)
	}
}

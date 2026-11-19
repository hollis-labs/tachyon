package localstore

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/libs/core/util/sqlite/sqlitekit"
)

// 055 rebuilds bus_events to admit the message and seat types: a database at 054 keeps every bus row
// with its id (the feed's cursor and the alert feed's bus_ ids depend on it), and the AUTOINCREMENT
// high-water mark survives, so an id freed by retention is never handed out again.
func TestEventSummaryMigrationKeepsBusRowsAndAdmitsMailAndSeats(t *testing.T) {
	ctx := context.Background()
	db, err := sqlitekit.OpenWriter(ctx, filepath.Join(t.TempDir(), "previous.db"), sqlitekit.OpenOptions{})
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
		if e.Name() >= "055" {
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
 INSERT INTO bus_events(id,tether_seq,tether_event_id,at_ns,type,subject,actor,app,severity,refs_json,card_json) VALUES
  (7,70,'e:70',1,'deploy','tachyon deployed','system','cerberus','info','[]',NULL),
  (9,90,'e:90',2,'task','CW-1 done','archie','torque','info','[{"kind":"task","ref":"CW-1","label":"CW-1"}]','{"title":"CW-1 done"}'),
  (20,200,'e:200',3,'deploy','newest, pruned','system','cerberus','info','[]',NULL);
 DELETE FROM bus_events WHERE id=20; -- retention took the newest row: the high-water mark (20) is above MAX(id) (9)
 INSERT INTO agents_events(session_id,tether_seq,tether_event_id,type,at_ns,actor,summary) VALUES('s1',5,'e:5','tool',1,'tether','tool.call.finished: Read')`); err != nil {
		t.Fatal(err)
	}
	if err = migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	var n, seq int
	if err = db.QueryRow(`SELECT COUNT(*), MAX(seq) FROM sqlite_sequence WHERE name='bus_events'`).Scan(&n, &seq); err != nil || n != 1 || seq != 20 {
		t.Fatal("the high-water mark must survive the rebuild as one sqlite_sequence row", n, seq, err)
	}
	if err = db.QueryRow(`SELECT COUNT(*) FROM sqlite_sequence WHERE name='bus_events_055'`).Scan(&n); err != nil || n != 0 {
		t.Fatal("the copy's sequence row must be renamed, not left behind", n, err)
	}
	var subject, refs, card, summary, cell string
	if err = db.QueryRow(`SELECT COUNT(*) FROM bus_events`).Scan(&n); err != nil || n != 2 {
		t.Fatal("bus rows lost", n, err)
	}
	if err = db.QueryRow(`SELECT subject,refs_json,card_json,tether_summary,cell FROM bus_events WHERE id=9 AND tether_seq=90`).Scan(&subject, &refs, &card, &summary, &cell); err != nil ||
		subject != "CW-1 done" || refs != `[{"kind":"task","ref":"CW-1","label":"CW-1"}]` || card != `{"title":"CW-1 done"}` || summary != "" || cell != "" {
		t.Fatal("bus row changed", subject, refs, card, summary, cell, err)
	}
	for _, typ := range []string{"message", "seat"} {
		if _, err = db.Exec(`INSERT INTO bus_events(tether_seq,tether_event_id,at_ns,type,subject,tether_summary,cell) VALUES(100,?,3,?,'x','x','photon')`, "e:"+typ, typ); err != nil {
			t.Fatal(typ, err)
		}
	}
	if _, err = db.Exec(`INSERT INTO bus_events(tether_seq,tether_event_id,at_ns,type,subject) VALUES(101,'e:bad',3,'bogus','x')`); err == nil {
		t.Fatal("the type CHECK must still hold")
	}
	var first int
	if err = db.QueryRow(`SELECT MIN(id) FROM bus_events WHERE type IN ('message','seat')`).Scan(&first); err != nil || first != 21 {
		t.Fatal("ids must continue past the old high-water mark, not past MAX(id)", first, err)
	}
	if err = db.QueryRow(`SELECT summary||'|'||tether_summary||'|'||cell FROM agents_events`).Scan(&summary); err != nil || summary != "tool.call.finished: Read||" {
		t.Fatal("agents_events must keep its derived summary and gain empty columns", summary, err)
	}
	for _, idx := range []string{"bus_events_seq", "bus_events_type"} {
		if err = db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name=?`, idx).Scan(&n); err != nil || n != 1 {
			t.Fatal("index missing", idx, err)
		}
	}
	if err = migrate(ctx, db); err != nil {
		t.Fatal("rerun", err)
	}
}

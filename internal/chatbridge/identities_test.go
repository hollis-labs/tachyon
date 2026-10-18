package chatbridge

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/hollis-labs/tachyon/internal/localstore"
	"github.com/hollis-labs/tachyon/internal/objects"
)

func TestChatIdentityIngestionReplayAndReadProjection(t *testing.T) {
	o, _, path := ownedTest(t)
	ctx := context.Background()
	if err := o.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	in, err := o.Inbox(ctx)
	if err != nil || len(in.Pending) != 1 || in.Pending[0].LocalSessionID == "" {
		t.Fatal("session identity missing", in, err)
	}
	turn := in.Pending[0]
	local := turn.LocalSessionID
	var original []byte
	if err = o.Store.Reader.QueryRow("SELECT body FROM chat_turns WHERE id=?", turn.ID).Scan(&original); err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	json.Unmarshal(original, &raw)
	if _, ok := raw["local_session_id"]; ok {
		t.Fatal("read projection leaked into original body")
	}
	service := objects.New(o.Store)
	session, err := service.Get(ctx, local)
	if err != nil || session.Source.Key.ExternalID != "s1" {
		t.Fatal("wrong session", session, err)
	}
	if err = o.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = o.BackfillSessionLinks(ctx, ""); err != nil {
		t.Fatal(err)
	}
	again, err := service.Get(ctx, local)
	if err != nil || again.Revision != session.Revision {
		t.Fatal("replay changed session revision", err)
	}
	page, err := o.SessionTurns(ctx, local, "", 1)
	if err != nil || len(page) != 1 || page[0].ID != turn.ID {
		t.Fatal("session history missing", err)
	}
	page, err = o.SessionTurns(ctx, local, turn.ID, 1)
	if err != nil || len(page) != 0 {
		t.Fatal("paging repeated turn", err)
	}
	if _, err = service.SetDeleted(ctx, local, session.Revision, true, "test"); err != nil {
		t.Fatal(err)
	}
	page, err = o.SessionTurns(ctx, local, "", 100)
	if err != nil || len(page) != 1 {
		t.Fatal("tombstone lost history", err)
	}
	reopened, err := localstore.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	other := Owned{Store: reopened, SourceID: o.SourceID}
	page, err = other.SessionTurns(ctx, local, "", 100)
	if err != nil || len(page) != 1 || page[0].LocalSessionID != local {
		t.Fatal("link lost on reopen", err)
	}
}

func TestBackfillPreservesOriginalsAndSeparatesInstances(t *testing.T) {
	o, _, _ := ownedTest(t)
	ctx := context.Background()
	// Seed pre-linkage data directly, including over one batch and a message
	// with no session identity. No launch/name heuristic may invent that link.
	err := o.Store.Writer.Submit(ctx, "seed_old_history", func(ctx context.Context, tx *sql.Tx) error {
		for i := 0; i < 205; i++ {
			id := fmt.Sprintf("old-%03d", i)
			body := fmt.Sprintf(`{"item_id":%q,"session_id":"same","content":"original","revision":7,"resolution":{"resolution_id":"reply","response_text":"preserve","resolved_at":"then"}}`, id)
			if _, err := tx.ExecContext(ctx, `INSERT INTO chat_turns VALUES(?,?,?,'','launch','same','then',7,?,1)`, id, o.SourceID, "channel", []byte(body)); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO chat_turns VALUES('other','other-hub','','','launch','same','then',1,'{"item_id":"other","session_id":"same"}',0);
INSERT INTO chat_turns VALUES('unknown','tether-test','','','launch','','then',1,'{"item_id":"unknown"}',0);
INSERT INTO chat_outbox VALUES('owned','sent','launch','operator message',1,'sent','','same');
INSERT INTO chat_outbox VALUES('owned','pending','launch','unknown session',2,'failed','','');
INSERT INTO chat_outbox VALUES('legacy','sent','launch','legacy original',1,'sent','','same');`); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	snapshot := func() string {
		var result string
		rows, err := o.Store.Reader.Query("SELECT id,body,revision,session_id FROM chat_turns ORDER BY id")
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		for rows.Next() {
			var id, body, session string
			var rev int
			if err = rows.Scan(&id, &body, &rev, &session); err != nil {
				t.Fatal(err)
			}
			result += fmt.Sprintf("%s:%s:%d:%s\n", id, body, rev, session)
		}
		if err = rows.Err(); err != nil {
			t.Fatal(err)
		}
		return result
	}
	before := snapshot()
	n, err := o.BackfillSessionLinks(ctx, "owned")
	if err != nil || n != 206 {
		t.Fatal("wrong backfill", n, err)
	}
	n, err = o.BackfillSessionLinks(ctx, "owned")
	if err != nil || n != 0 {
		t.Fatal("backfill not idempotent", n, err)
	}
	if snapshot() != before {
		t.Fatal("original history changed")
	}
	other := Owned{Store: o.Store, SourceID: "other-hub"}
	if _, err = other.BackfillSessionLinks(ctx, ""); err != nil {
		t.Fatal(err)
	}
	var a, b string
	if err = o.Store.Reader.QueryRow("SELECT session_object_id FROM chat_session_links WHERE turn_id='old-000'").Scan(&a); err != nil {
		t.Fatal(err)
	}
	if err = o.Store.Reader.QueryRow("SELECT session_object_id FROM chat_session_links WHERE turn_id='other'").Scan(&b); err != nil || a == b {
		t.Fatal("instance collision", err)
	}
	var count int
	if err = o.Store.Reader.QueryRow("SELECT count(*) FROM chat_outbox_session_links").Scan(&count); err != nil || count != 1 {
		t.Fatal("unknown/legacy outbox inferred", err)
	}
	page, err := other.SessionTurns(ctx, a, "", 100)
	if err != nil || len(page) != 0 {
		t.Fatal("cross-instance history exposed", err)
	}
}

func TestTurnAndSessionLinkRollbackTogether(t *testing.T) {
	o, _, _ := ownedTest(t)
	ctx := context.Background()
	err := o.Store.Writer.Submit(ctx, "rollback", func(ctx context.Context, tx *sql.Tx) error {
		if err := o.saveTurn(ctx, tx, Turn{ID: "rollback", SessionID: "rollback-session", Revision: 1, Created: time.Now().UTC().Format(time.RFC3339Nano)}, false); err != nil {
			return err
		}
		return fmt.Errorf("abort")
	})
	if err == nil {
		t.Fatal("expected rollback")
	}
	for _, table := range []string{"chat_turns", "chat_session_links", "objects", "object_revisions"} {
		var n int
		if err = o.Store.Reader.QueryRow("SELECT count(*) FROM " + table).Scan(&n); err != nil || n != 0 {
			t.Fatal("partial commit", table, n, err)
		}
	}
}

package chatbridge

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/hollis-labs/tachyon/next/internal/store/storetest"
)

// The outbox list on either dialect: one owner's rows for one launch, newest first, with the local
// session a sent row was linked to.
func TestOutboxList(t *testing.T) {
	ctx := context.Background()
	db := storetest.New(t).Open(t)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Lib().ExecContext(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	me := OutboxIdentity("https://tether.example.test")
	exec(`INSERT INTO objects(id, kind, ownership, name, revision, local_json, metadata_json, created_at, updated_at) VALUES ('sess-1', 'session', 'tachyon', 's', 1, '{}', '{}', 't', 't')`)
	exec(`INSERT INTO chat_outbox(identity, id, launch_id, body, created_at, status, error, session_id) VALUES (?, 'k1', 'pm', 'one', 1, 'sent', '', 's1'), (?, 'k2', 'pm', 'two', 2, 'failed', 'boom', ''), (?, 'k3', 'pm', 'other owner', 3, 'sent', '', '')`,
		me, me, OutboxIdentity("other"))
	exec(`INSERT INTO chat_outbox_session_links(identity, id, session_object_id, linked_at) VALUES (?, 'k1', 'sess-1', 't')`, me)
	rec := httptest.NewRecorder()
	OutboxList(db, me).ServeHTTP(rec, httptest.NewRequest("GET", "/api/plugins/console/v1/chat"+OutboxPath+"?launch_id=pm", nil))
	var got []Sent
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || rec.Code != 200 {
		t.Fatalf("%d %s %v", rec.Code, rec.Body, err)
	}
	if len(got) != 2 || got[0].Key != "k2" || got[0].Error != "boom" || got[1].LocalSessionID != "sess-1" {
		t.Fatalf("outbox = %+v", got)
	}
}

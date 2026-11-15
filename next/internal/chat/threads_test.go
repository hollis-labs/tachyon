package chat

import (
	"context"
	"errors"
	"testing"

	"github.com/hollis-labs/tachyon/next/internal/store/storetest"
)

func TestLocalThreads(t *testing.T) {
	ctx := context.Background()
	db := storetest.New(t).Open(t)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Lib().ExecContext(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO app_agent_threads(id, agent, title, created_ns, updated_ns) VALUES (?, ?, ?, ?, ?)`, "a1", "pm", "T", 1, 1)
	exec(`INSERT INTO chat_turns(id, source, channel, publication_id, launch_id, session_id, created_at, revision, body) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"turn1", "tether", "c", "", "l", "s-turns", "2026-01-01T00:00:00Z", 1, []byte("{}"))
	exec(`INSERT INTO chat_session_lineage(session_id, agent_id, seen_ns) VALUES (?, ?, ?)`, "s-lineage", "pm", 1)
	exec(`INSERT INTO chat_thread_state(thread_ref, updated_ns) VALUES (?, ?)`, "tether:s-state", 1)
	exists := LocalThreads(db)
	for ref, want := range map[string]bool{
		"app:a1": true, "app:nope": false,
		"tether:s-turns": true, "tether:s-lineage": true, "tether:s-state": true, "tether:nope": false,
		"bogus": false, "app:": false,
	} {
		err := exists(ctx, ref)
		if want && err != nil || !want && !errors.Is(err, ErrNoThread) {
			t.Errorf("%s: %v, want exists=%v", ref, err, want)
		}
	}
}

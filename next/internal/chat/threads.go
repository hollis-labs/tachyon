// Package chat holds what the chat packages share in the port: the thread-existence check.
package chat

import (
	"context"
	"database/sql"
	"errors"

	"github.com/hollis-labs/tachyon/next/internal/chat/chatapi"
	"github.com/hollis-labs/tachyon/next/internal/store"
)

// ErrNoThread is the answer for a thread this build does not know.
var ErrNoThread = errors.New("chat: no such thread")

// LocalThreads is the thread-existence hook the chat handlers take (Exists), answered from the
// local tables. The old build asked the threads aggregator, which also lists live Tether sessions;
// the threads slice (B6b) restores that. Until then a thread exists when the local data knows it:
//   - app:<id> has its app_agent_threads row;
//   - tether:<session> has synced turns (chat_turns), a lineage row (chat_session_lineage) or
//     thread state (chat_thread_state).
func LocalThreads(db *store.Store) func(ctx context.Context, ref string) error {
	return func(ctx context.Context, ref string) error {
		t, err := chatapi.ParseRef(ref)
		if err != nil {
			return ErrNoThread
		}
		var q string
		var args []any
		switch t.Source {
		case chatapi.SourceApp:
			q, args = `SELECT 1 FROM app_agent_threads WHERE id=?`, []any{t.ID}
		case chatapi.SourceTether:
			q = `SELECT 1 FROM chat_turns WHERE session_id=?
 UNION ALL SELECT 1 FROM chat_session_lineage WHERE session_id=?
 UNION ALL SELECT 1 FROM chat_thread_state WHERE thread_ref=?`
			args = []any{t.ID, t.ID, ref}
		default:
			return ErrNoThread
		}
		var one int
		err = db.Reader.QueryRowContext(ctx, q+` LIMIT 1`, args...).Scan(&one)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNoThread
		}
		return err
	}
}

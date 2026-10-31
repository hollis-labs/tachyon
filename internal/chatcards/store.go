// Package chatcards binds Approvals queue items to chat threads (T5): the card.ask context, the card
// projection route for the thread stream, the thread's open-card count, and the message that tells the
// agent how a card was answered. Answers still go through the Approvals resolve route; there is no
// second resolver here. Design: design_tachyon_chat_backend_20261029 section 5.
package chatcards

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/hollis-labs/tachyon/internal/localstore"
)

// Ref is one bound card.
type Ref struct {
	ItemID     string
	ThreadRef  string
	MessageRef string
	Kind       string
	CreatedNS  int64
}

// Store keeps chat_card_refs.
type Store struct {
	DB  *localstore.Store
	Now func() time.Time
}

func (s *Store) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// ErrBound means the item is already bound to a different thread.
var ErrBound = errors.New("card is bound to another thread")

// Bind records the item against the thread. Binding the same item to the same thread again is a no-op,
// so a retried card.ask is safe.
func (s *Store) Bind(ctx context.Context, itemID, thread, message, kind string) error {
	return s.DB.Writer.Submit(ctx, "chat_card_bind", func(ctx context.Context, tx *sql.Tx) error {
		var cur string
		err := tx.QueryRowContext(ctx, `SELECT thread_ref FROM chat_card_refs WHERE item_id = ?`, itemID).Scan(&cur)
		switch {
		case err == nil && cur != thread:
			return ErrBound
		case err == nil:
			return nil
		case !errors.Is(err, sql.ErrNoRows):
			return err
		}
		var msg any
		if message != "" {
			msg = message
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO chat_card_refs(item_id, thread_ref, message_ref, kind, created_ns) VALUES (?,?,?,?,?)`,
			itemID, thread, msg, kind, s.now().UnixNano())
		return err
	})
}

// Get returns the binding of one item.
func (s *Store) Get(ctx context.Context, itemID string) (Ref, bool) {
	var r Ref
	var msg sql.NullString
	err := s.DB.Reader.QueryRowContext(ctx, `SELECT item_id, thread_ref, message_ref, kind, created_ns FROM chat_card_refs WHERE item_id = ?`, itemID).
		Scan(&r.ItemID, &r.ThreadRef, &msg, &r.Kind, &r.CreatedNS)
	r.MessageRef = msg.String
	return r, err == nil
}

// ForThread lists the thread's cards, oldest first; since is a created_ns lower bound (exclusive).
func (s *Store) ForThread(ctx context.Context, thread string, since int64, limit int) ([]Ref, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	rows, err := s.DB.Reader.QueryContext(ctx, `SELECT item_id, thread_ref, message_ref, kind, created_ns FROM chat_card_refs WHERE thread_ref = ? AND created_ns > ? ORDER BY created_ns LIMIT ?`, thread, since, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Ref
	for rows.Next() {
		var r Ref
		var msg sql.NullString
		if err := rows.Scan(&r.ItemID, &r.ThreadRef, &msg, &r.Kind, &r.CreatedNS); err != nil {
			return nil, err
		}
		r.MessageRef = msg.String
		out = append(out, r)
	}
	return out, rows.Err()
}

// OpenCounts returns, per thread ref, how many bound cards are still waiting for an answer.
func (s *Store) OpenCounts(ctx context.Context, refs []string) map[string]int {
	return s.openCounts(ctx, refs, "")
}

// OpenApprovalCounts is OpenCounts limited to approval-like cards (approvals, tool approvals, plans, dispatches).
func (s *Store) OpenApprovalCounts(ctx context.Context, refs []string) map[string]int {
	return s.openCounts(ctx, refs, ` AND (c.kind LIKE '%approval%' OR c.kind IN ('card.plan','card.dispatch'))`)
}

func (s *Store) openCounts(ctx context.Context, refs []string, extra string) map[string]int {
	out := map[string]int{}
	if len(refs) == 0 {
		return out
	}
	args := make([]any, len(refs))
	ph := ""
	for i, r := range refs {
		args[i] = r
		if i > 0 {
			ph += ","
		}
		ph += "?"
	}
	rows, err := s.DB.Reader.QueryContext(ctx, `SELECT c.thread_ref, COUNT(*) FROM chat_card_refs c JOIN approval_items a ON a.id = c.item_id
 WHERE c.thread_ref IN (`+ph+`) AND a.state IN ('submitted','validated','staged','presented')`+extra+` GROUP BY c.thread_ref`, args...)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var ref string
		var n int
		if rows.Scan(&ref, &n) == nil {
			out[ref] = n
		}
	}
	return out
}

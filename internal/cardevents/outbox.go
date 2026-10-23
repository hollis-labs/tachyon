// Package cardevents publishes a card's lifecycle to the hub as card.*
// events. The queue stays the owner of card state; events are notifications of
// ids, codes and times only (no titles, data, answers or responder identity).
// An outbox makes delivery at-least-once with stable event ids, so the hub's
// dedupe makes it effectively once.
package cardevents

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/hollis-labs/tachyon/internal/localstore"
)

// Outbox states.
const (
	StatePending  = "pending"
	StateSent     = "sent"
	StateRejected = "rejected" // the hub refused it for good; never retried
)

// Row is one queued event.
type Row struct {
	Seq     int64
	ItemID  string
	Kind    string
	EventID string
	Event   json.RawMessage
}

// Store persists the outbox through Tachyon's one SQLite writer.
type Store struct{ DB *localstore.Store }

// Queue adds an event unless one of that kind already exists for the item. The
// build function receives the new row's sequence and event id so both are in
// the stored event. It reports whether a row was added.
func (s *Store) Queue(ctx context.Context, itemID, kind, eventID string, build func(seq int64) ([]byte, error), at time.Time) (bool, error) {
	added := false
	err := s.DB.Writer.Submit(ctx, "card_event_queue", func(ctx context.Context, tx *sql.Tx) error {
		var n int
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM card_event_outbox WHERE item_id=? AND kind=?", itemID, kind).Scan(&n); err != nil || n > 0 {
			return err
		}
		var last sql.NullInt64
		if err := tx.QueryRowContext(ctx, "SELECT MAX(seq) FROM card_event_outbox").Scan(&last); err != nil {
			return err
		}
		seq := last.Int64 + 1
		raw, err := build(seq)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, "INSERT INTO card_event_outbox(seq,item_id,kind,event_id,event,state,created_ns) VALUES(?,?,?,?,?,?,?)", seq, itemID, kind, eventID, string(raw), StatePending, at.UnixNano())
		added = err == nil
		return err
	})
	return added, err
}

// Pending lists unsent events in sequence order.
func (s *Store) Pending(ctx context.Context, limit int) ([]Row, error) {
	rows, err := s.DB.Reader.QueryContext(ctx, "SELECT seq,item_id,kind,event_id,event FROM card_event_outbox WHERE state=? ORDER BY seq LIMIT ?", StatePending, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Row
	for rows.Next() {
		var r Row
		var ev string
		if err := rows.Scan(&r.Seq, &r.ItemID, &r.Kind, &r.EventID, &ev); err != nil {
			return nil, err
		}
		r.Event = json.RawMessage(ev)
		out = append(out, r)
	}
	return out, rows.Err()
}

// Mark records the outcome of one event.
func (s *Store) Mark(ctx context.Context, seq int64, state, code string, at time.Time) error {
	return s.DB.Writer.Submit(ctx, "card_event_mark", func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "UPDATE card_event_outbox SET state=?,reject_code=?,sent_ns=? WHERE seq=?", state, code, at.UnixNano(), seq)
		return err
	})
}

// Done marks an item as fully queued.
func (s *Store) Done(ctx context.Context, itemID string) error {
	return s.DB.Writer.Submit(ctx, "card_event_scan_done", func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "INSERT OR IGNORE INTO card_event_scan(item_id) VALUES(?)", itemID)
		return err
	})
}

// Unscanned lists item ids not yet fully queued, oldest first.
func (s *Store) Unscanned(ctx context.Context, limit int) ([]string, error) {
	rows, err := s.DB.Reader.QueryContext(ctx, "SELECT id FROM approval_items WHERE id NOT IN (SELECT item_id FROM card_event_scan) ORDER BY created_ns LIMIT ?", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

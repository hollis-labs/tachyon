// Package carddelivery pushes the outcome of a card to the place its routing
// profile names. Pull delivery (card.await, card.get) always works; this adds
// one durable push per card and revision, chosen by the profile's rules.
package carddelivery

import (
	"context"
	"database/sql"
	"time"

	"github.com/hollis-labs/tachyon/internal/localstore"
)

// Delivery states.
const (
	StatePending = "pending" // claimed, not yet accepted by the sink; retried
	StateSent    = "sent"    // the sink accepted it
	StateHeld    = "held"    // the route says the asker pulls (delivery=await); nothing was sent
	StateFailed  = "failed"  // gave up: unknown sink, or attempts exhausted
)

// Row is one delivery record.
type Row struct {
	ItemID, Sink, RuleID, ProfileHash, Completion, Delivery, State, Key, Receipt, Error string
	Revision                                                                            int64
	Attempts                                                                            int
}

// Store persists deliveries through Tachyon's one SQLite writer.
type Store struct{ DB *localstore.Store }

// Claim reserves (item, revision). It reports false when it is already claimed,
// so one card revision has one primary delivery.
func (s *Store) Claim(ctx context.Context, r Row, at time.Time) (bool, error) {
	won := false
	err := s.DB.Writer.Submit(ctx, "card_delivery_claim", func(ctx context.Context, tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO card_deliveries(item_id,revision,sink,rule_id,profile_hash,completion,delivery,state,idem_key,created_ns,next_ns)
 VALUES(?,?,?,?,?,?,?,?,?,?,?)`, r.ItemID, r.Revision, r.Sink, r.RuleID, r.ProfileHash, r.Completion, r.Delivery, r.State, r.Key, at.UnixNano(), at.UnixNano())
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		won = n == 1
		return nil
	})
	return won, err
}

// Settle records the outcome of an attempt. A pending result keeps the row
// retryable after backoff; any other state ends it.
func (s *Store) Settle(ctx context.Context, itemID string, revision int64, state, receipt, errText string, retryAt, at time.Time) error {
	return s.DB.Writer.Submit(ctx, "card_delivery_settle", func(ctx context.Context, tx *sql.Tx) error {
		var ended any
		if state != StatePending {
			ended = at.UnixNano()
		}
		_, err := tx.ExecContext(ctx, `UPDATE card_deliveries SET state=?,receipt=?,error=?,attempts=attempts+?,next_ns=?,ended_ns=? WHERE item_id=? AND revision=?`,
			state, receipt, errText, 1, retryAt.UnixNano(), ended, itemID, revision)
		return err
	})
}

// Due lists pending deliveries whose backoff has passed.
func (s *Store) Due(ctx context.Context, at time.Time, limit int) ([]Row, error) {
	rows, err := s.DB.Reader.QueryContext(ctx, `SELECT item_id,revision,sink,rule_id,profile_hash,completion,delivery,state,idem_key,receipt,error,attempts
 FROM card_deliveries WHERE state='pending' AND next_ns<=? ORDER BY created_ns LIMIT ?`, at.UnixNano(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Row
	for rows.Next() {
		var r Row
		if err := rows.Scan(&r.ItemID, &r.Revision, &r.Sink, &r.RuleID, &r.ProfileHash, &r.Completion, &r.Delivery, &r.State, &r.Key, &r.Receipt, &r.Error, &r.Attempts); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Get returns the latest delivery for an item, or nil.
func (s *Store) Get(ctx context.Context, itemID string) (*Row, error) {
	var r Row
	err := s.DB.Reader.QueryRowContext(ctx, `SELECT item_id,revision,sink,rule_id,profile_hash,completion,delivery,state,idem_key,receipt,error,attempts
 FROM card_deliveries WHERE item_id=? ORDER BY revision DESC LIMIT 1`, itemID).
		Scan(&r.ItemID, &r.Revision, &r.Sink, &r.RuleID, &r.ProfileHash, &r.Completion, &r.Delivery, &r.State, &r.Key, &r.Receipt, &r.Error, &r.Attempts)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &r, err
}

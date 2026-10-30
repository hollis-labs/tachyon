// Package cardfx runs the effects of an approved card. A card lists effects
// and carries an op_digest over them; once the operator approves, the executor
// applies exactly that set against an allowlist of targets, once, and records a
// receipt the asker and the review dialog can read.
package cardfx

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/hollis-labs/tachyon/internal/localstore"
)

// Run states of one card's effect set.
const (
	StateRunning   = "running"
	StateCompleted = "completed" // every effect applied
	StateFailed    = "failed"    // one failed; the rest were skipped
	StateRefused   = "refused"   // nothing ran: digest mismatch, unknown target or invalid effect
	StateNone      = "none"      // nothing to run: denied, or no effects
)

// EffectResult is the outcome of one effect.
type EffectResult struct {
	Index  int    `json:"index"`
	Target string `json:"target"`
	Op     string `json:"op,omitempty"`
	Status string `json:"status"` // ok | failed | skipped
	Detail string `json:"detail,omitempty"`
	Error  string `json:"error,omitempty"`
}

// Receipt is what happened after approval, including who approved and the note.
type Receipt struct {
	ItemID   string `json:"item_id"`
	State    string `json:"state"`
	OpDigest string `json:"op_digest,omitempty"`
	Decision string `json:"decision,omitempty"`
	// Alternative is the id of the alternative the operator chose instead of the main effects.
	Alternative string         `json:"alternative_id,omitempty"`
	Approver    string         `json:"approver,omitempty"`
	Assurance   string         `json:"assurance,omitempty"`
	Note        string         `json:"note,omitempty"`
	ApprovedAt  *time.Time     `json:"approved_at,omitempty"`
	Reason      string         `json:"reason,omitempty"`
	Effects     []EffectResult `json:"effects,omitempty"`
	StartedAt   time.Time      `json:"started_at"`
	EndedAt     *time.Time     `json:"ended_at,omitempty"`
}

// Store persists effect runs through Tachyon's one SQLite writer.
type Store struct{ DB *localstore.Store }

// Claim reserves an item's effect run. It reports false when another pass
// already claimed it, so a set of effects starts at most once.
func (s *Store) Claim(ctx context.Context, itemID string, at time.Time) (bool, error) {
	won := false
	err := s.DB.Writer.Submit(ctx, "card_effects_claim", func(ctx context.Context, tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, "INSERT OR IGNORE INTO card_effect_runs(item_id,state,created_ns) VALUES(?,?,?)", itemID, StateRunning, at.UnixNano())
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		won = n == 1
		return nil
	})
	return won, err
}

// Finish records the receipt and the terminal state.
func (s *Store) Finish(ctx context.Context, r Receipt, at time.Time) error {
	r.EndedAt = &at
	raw, err := json.Marshal(r)
	if err != nil {
		return err
	}
	return s.DB.Writer.Submit(ctx, "card_effects_finish", func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "UPDATE card_effect_runs SET state=?,receipt=?,ended_ns=? WHERE item_id=?", r.State, string(raw), at.UnixNano(), r.ItemID)
		return err
	})
}

// Get returns an item's receipt, or nil when none exists yet. A run still in
// flight reads as a receipt in the running state.
func (s *Store) Get(ctx context.Context, itemID string) (*Receipt, error) {
	var state, raw string
	var created int64
	err := s.DB.Reader.QueryRowContext(ctx, "SELECT state,receipt,created_ns FROM card_effect_runs WHERE item_id=?", itemID).Scan(&state, &raw, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if raw == "" {
		return &Receipt{ItemID: itemID, State: state, StartedAt: time.Unix(0, created).UTC()}, nil
	}
	var r Receipt
	if err := json.Unmarshal([]byte(raw), &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// Interrupted lists runs a crash left in flight.
func (s *Store) Interrupted(ctx context.Context) ([]string, error) {
	rows, err := s.DB.Reader.QueryContext(ctx, "SELECT item_id FROM card_effect_runs WHERE state=?", StateRunning)
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

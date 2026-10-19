package approvals

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/hollis-labs/libs/mesh/core/hitl"
	"github.com/hollis-labs/tachyon/internal/localstore"
)

// Store retains native HITL records through Tachyon's one SQLite writer.
type Store struct{ DB *localstore.Store }

var _ hitl.Store = (*Store)(nil)

type diskRecord struct {
	hitl.Record
	Outcome json.RawMessage
}

func recordBytes(rec hitl.Record) ([]byte, error) {
	var outcome json.RawMessage
	if rec.Outcome != nil {
		raw, err := json.Marshal(rec.Outcome)
		if err != nil {
			return nil, err
		}
		outcome = raw
	}
	return json.Marshal(diskRecord{Record: rec, Outcome: outcome})
}

type rowScanner interface{ Scan(...any) error }

func readRecord(row rowScanner) (hitl.Record, error) {
	var raw []byte
	if err := row.Scan(&raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return hitl.Record{}, hitl.ErrNotFound
		}
		return hitl.Record{}, err
	}
	var saved diskRecord
	if err := json.Unmarshal(raw, &saved); err != nil {
		return hitl.Record{}, err
	}
	if len(saved.Outcome) > 0 && string(saved.Outcome) != "null" {
		outcome, err := hitl.UnmarshalOutcome(saved.Outcome)
		if err != nil {
			return hitl.Record{}, err
		}
		saved.Record.Outcome = outcome
	}
	return saved.Record, nil
}
func (s *Store) Get(ctx context.Context, id string) (hitl.Record, error) {
	return readRecord(s.DB.Reader.QueryRowContext(ctx, "SELECT record FROM approval_items WHERE id=?", id))
}
func (s *Store) Create(ctx context.Context, rec hitl.Record) (hitl.Record, bool, error) {
	if err := rec.Validate(); err != nil {
		return hitl.Record{}, false, err
	}
	raw, err := recordBytes(rec)
	if err != nil {
		return hitl.Record{}, false, err
	}
	var out hitl.Record
	created := false
	err = s.DB.Writer.Submit(ctx, "approval_enqueue", func(ctx context.Context, tx *sql.Tx) error {
		old, err := readRecord(tx.QueryRowContext(ctx, "SELECT record FROM approval_items WHERE caller_scope=? AND idempotency_key=?", rec.CallerScope, rec.IdempotencyKey))
		if err == nil {
			out = old
			return nil
		}
		if !errors.Is(err, hitl.ErrNotFound) {
			return err
		}
		var exists bool
		if err = tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM approval_items WHERE id=?)", rec.ItemID).Scan(&exists); err != nil {
			return err
		}
		if exists {
			return hitl.ErrInvalidRecord
		}
		var expires any
		if rec.ExpiresAt != nil {
			expires = rec.ExpiresAt.UnixNano()
		}
		_, err = tx.ExecContext(ctx, "INSERT INTO approval_items(id,caller_scope,idempotency_key,state,revision,terminal,expires_ns,created_ns,record) VALUES(?,?,?,?,?,?,?,?,?)", rec.ItemID, rec.CallerScope, rec.IdempotencyKey, rec.State, rec.Revision, rec.State.IsTerminal(), expires, rec.CreatedAt.UnixNano(), raw)
		if err == nil {
			out, err = readRecord(tx.QueryRowContext(ctx, "SELECT record FROM approval_items WHERE id=?", rec.ItemID))
			created = err == nil
		}
		return err
	})
	return out, created, err
}
func (s *Store) Swap(ctx context.Context, id string, expected int64, next hitl.Record) error {
	return s.DB.Writer.Submit(ctx, "approval_transition", func(ctx context.Context, tx *sql.Tx) error {
		prev, err := readRecord(tx.QueryRowContext(ctx, "SELECT record FROM approval_items WHERE id=?", id))
		if err != nil {
			return err
		}
		if err = hitl.CheckSwap(prev, expected, next); err != nil {
			return err
		}
		raw, err := recordBytes(next)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, "UPDATE approval_items SET state=?,revision=?,terminal=?,record=? WHERE id=?", next.State, next.Revision, next.State.IsTerminal(), raw, id)
		return err
	})
}
func (s *Store) DueForExpiry(ctx context.Context, now time.Time, limit int) ([]hitl.Record, error) {
	if limit <= 0 {
		limit = -1
	}
	rows, err := s.DB.Reader.QueryContext(ctx, "SELECT record FROM approval_items WHERE terminal=0 AND expires_ns<=? ORDER BY expires_ns,id LIMIT ?", now.UnixNano(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []hitl.Record{}
	for rows.Next() {
		rec, err := readRecord(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

// Page includes logically expired items in history even before materialization.
func (s *Store) Page(ctx context.Context, history bool, before string, limit int) ([]hitl.Record, string, error) {
	if limit <= 0 || limit > 100 {
		limit = 25
	}
	predicate := "(terminal=1 OR expires_ns<=?)"
	if !history {
		predicate = "(terminal=0 AND (expires_ns IS NULL OR expires_ns>?))"
	}
	query := "SELECT record FROM approval_items WHERE " + predicate
	args := []any{time.Now().UnixNano()}
	if before != "" {
		cursor, err := s.Get(ctx, before)
		if err != nil {
			return nil, "", hitl.ErrInvalidRequest
		}
		query += " AND (created_ns,id)<(?,?)"
		args = append(args, cursor.CreatedAt.UnixNano(), cursor.ItemID)
	}
	query += " ORDER BY created_ns DESC,id DESC LIMIT ?"
	args = append(args, limit+1)
	rows, err := s.DB.Reader.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	out := []hitl.Record{}
	for rows.Next() {
		rec, err := readRecord(rows)
		if err != nil {
			return nil, "", err
		}
		out = append(out, rec)
	}
	next := ""
	if len(out) > limit {
		out = out[:limit]
		next = out[len(out)-1].ItemID
	}
	return out, next, rows.Err()
}

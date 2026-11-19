package approvals

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/hollis-labs/libs/mesh/core/hitl"
	"github.com/hollis-labs/tachyon/next/internal/store"
)

// Store retains native HITL records through Tachyon's one SQLite writer.
type Store struct{ DB *store.Store }

var _ hitl.Store = (*Store)(nil)

type diskRecord struct {
	hitl.Record
	Outcome      json.RawMessage
	NativeCommit json.RawMessage `json:"native_commit,omitempty"`
}

func recordBytes(rec hitl.Record) ([]byte, error) { return recordBytesWithEvidence(rec, nil) }
func recordBytesWithEvidence(rec hitl.Record, evidence []byte) ([]byte, error) {
	var outcome json.RawMessage
	if rec.Outcome != nil {
		raw, err := json.Marshal(rec.Outcome)
		if err != nil {
			return nil, err
		}
		outcome = raw
	}
	return json.Marshal(diskRecord{Record: rec, Outcome: outcome, NativeCommit: evidence})
}

type rowScanner interface{ Scan(...any) error }

func readStoredRecord(row rowScanner) (hitl.Record, []byte, []byte, error) {
	var raw []byte
	if err := row.Scan(&raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return hitl.Record{}, nil, nil, hitl.ErrNotFound
		}
		return hitl.Record{}, nil, nil, err
	}
	var saved diskRecord
	if err := json.Unmarshal(raw, &saved); err != nil {
		return hitl.Record{}, nil, nil, err
	}
	if len(saved.Outcome) > 0 && string(saved.Outcome) != "null" {
		outcome, err := hitl.UnmarshalOutcome(saved.Outcome)
		if err != nil {
			return hitl.Record{}, nil, nil, err
		}
		saved.Record.Outcome = outcome
	}
	canonical, err := recordBytes(saved.Record)
	if err != nil {
		return hitl.Record{}, nil, nil, err
	}
	return saved.Record, canonical, saved.NativeCommit, nil
}
func readRecord(row rowScanner) (hitl.Record, error) {
	rec, raw, evidence, err := readStoredRecord(row)
	if err != nil {
		return hitl.Record{}, err
	}
	if err = validateNativeCommit(rec, raw, evidence); err != nil {
		return hitl.Record{}, err
	}
	return rec, nil
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
	err = s.DB.Writer.Submit(ctx, "approval_enqueue", func(ctx context.Context, tx store.Tx) error {
		old, ierr := readRecord(tx.QueryRowContext(ctx, "SELECT record FROM approval_items WHERE caller_scope=? AND idempotency_key=?", rec.CallerScope, rec.IdempotencyKey))
		if ierr == nil {
			out = old
			return nil
		}
		if !errors.Is(ierr, hitl.ErrNotFound) {
			return ierr
		}
		var exists bool
		if ierr = tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM approval_items WHERE id=?)", rec.ItemID).Scan(&exists); ierr != nil {
			return ierr
		}
		if exists {
			return hitl.ErrInvalidRecord
		}
		var expires any
		if rec.ExpiresAt != nil {
			expires = rec.ExpiresAt.UnixNano()
		}
		_, ierr = tx.ExecContext(ctx, "INSERT INTO approval_items(id,caller_scope,idempotency_key,state,revision,terminal,expires_ns,created_ns,record) VALUES(?,?,?,?,?,?,?,?,?)", rec.ItemID, rec.CallerScope, rec.IdempotencyKey, rec.State, rec.Revision, flag(rec.State.IsTerminal()), expires, rec.CreatedAt.UnixNano(), raw)
		if ierr == nil {
			out, ierr = readRecord(tx.QueryRowContext(ctx, "SELECT record FROM approval_items WHERE id=?", rec.ItemID))
			created = ierr == nil
			if created {
				indexTx(ctx, tx, out)
			}
		}
		return ierr
	})
	if err == nil && created {
		s.publish(out, true)
	}
	return out, created, err
}
func (s *Store) Swap(ctx context.Context, id string, expected int64, next hitl.Record) error {
	if next.Kind == NativeKind && next.State == hitl.StateResolved {
		return s.swapNative(ctx, id, expected, next)
	}
	err := s.DB.Writer.Submit(ctx, "approval_transition", func(ctx context.Context, tx store.Tx) error {
		// A native response's context is bounded by its callback authorization.
		// Recheck after acquiring the writer, and before committing its changes.
		if err := checkWriteDeadline(ctx); err != nil {
			return err
		}
		if err := swapRecord(ctx, tx, id, expected, next); err != nil {
			return err
		}
		return checkWriteDeadline(ctx)
	})
	if err == nil {
		s.publish(next, false)
	}
	return err
}

func checkWriteDeadline(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	// The timer that cancels a context can be delayed by scheduling. Compare the
	// actual deadline too, so a queued CAS cannot use that delay as extra time.
	if deadline, ok := ctx.Deadline(); ok && !time.Now().Before(deadline) {
		return context.DeadlineExceeded
	}
	return nil
}

func swapRecord(ctx context.Context, tx store.Tx, id string, expected int64, next hitl.Record) error {
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
	if err = checkWriteDeadline(ctx); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "UPDATE approval_items SET state=?,revision=?,terminal=?,record=? WHERE id=?", next.State, next.Revision, flag(next.State.IsTerminal()), raw, id)
	if err == nil {
		indexTx(ctx, tx, next)
		logTerminalTx(ctx, tx, next)
	}
	return err
}
func (s *Store) DueForExpiry(ctx context.Context, now time.Time, limit int) ([]hitl.Record, error) {
	if limit <= 0 {
		limit = -1
	}
	rows, err := s.DB.Reader.QueryContext(ctx, "SELECT record FROM approval_items WHERE terminal=0 AND expires_ns<=? ORDER BY expires_ns,id LIMIT ?", now.UnixNano(), limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
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
		cursor, _, _, err := readStoredRecord(s.DB.Reader.QueryRowContext(ctx, "SELECT record FROM approval_items WHERE id=?", before))
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
	defer func() { _ = rows.Close() }()
	out := []hitl.Record{}
	var scanned []string
	for rows.Next() {
		rec, raw, evidence, err := readStoredRecord(rows)
		if err != nil {
			return nil, "", err
		}
		scanned = append(scanned, rec.ItemID)
		if len(scanned) > limit {
			break
		}
		if errors.Is(validateNativeCommit(rec, raw, evidence), ErrNativeUnconfirmed) {
			continue
		}
		out = append(out, rec)
	}
	next := ""
	if len(scanned) > limit {
		next = scanned[limit-1]
	}
	return out, next, rows.Err()
}

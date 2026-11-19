package alertstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"slices"
	"strings"
	"time"

	"github.com/hollis-labs/tachyon/internal/alertsapi"
)

// Op is one operator decision. ExpectedVersion is required for single-alert ops.
type Op struct {
	Op              string
	Preset          string
	UntilMS         *int64
	UntilRefChange  *alertsapi.UntilRef
	Note            string
	Resolution      string
	ExpectedVersion *int64
}

const maxSnooze = 30 * 24 * time.Hour

var presets = map[string]time.Duration{"15m": 15 * time.Minute, "1h": time.Hour, "4h": 4 * time.Hour, "tomorrow": 18 * time.Hour}

// apply mutates a for op at now and returns a message when the op is invalid.
func apply(a *alertsapi.Alert, o Op, nowMS int64) string {
	if len(o.Note) > alertsapi.NoteMax {
		return "note is too long."
	}
	note := strings.TrimSpace(o.Note)
	clear := func() { a.SnoozeUntil, a.WakeAtMS, a.Defer = nil, nil, nil }
	switch o.Op {
	case "read":
		a.Read = true
	case "unread":
		a.Read = false
	case "dismiss":
		a.Status, a.Read, a.Note, a.ResolvedBy = "dismissed", true, note, "operator"
		clear()
	case "snooze":
		var until int64
		switch {
		case o.Preset != "" && o.UntilMS != nil:
			return "Give preset or until_ms, not both."
		case o.Preset != "":
			d, ok := presets[o.Preset]
			if !ok {
				return "preset must be one of " + strings.Join(alertsapi.SnoozePresets, ", ") + "."
			}
			until = nowMS + d.Milliseconds()
		case o.UntilMS != nil:
			until = *o.UntilMS
			if until <= nowMS || until > nowMS+maxSnooze.Milliseconds() {
				return "until_ms must be in the future and within 30 days."
			}
		default:
			return "snooze needs preset or until_ms."
		}
		a.Status, a.Read, a.Note = "snoozed", true, note
		a.SnoozeUntil, a.WakeAtMS, a.Defer = &until, &until, nil
	case "defer":
		if o.UntilMS == nil && o.UntilRefChange == nil {
			return "defer needs until_ms or until_ref_change."
		}
		if o.UntilMS != nil && (*o.UntilMS <= nowMS || *o.UntilMS > nowMS+maxSnooze.Milliseconds()) {
			return "until_ms must be in the future and within 30 days."
		}
		if r := o.UntilRefChange; r != nil && (r.Kind == "" || r.Ref == "" || len(r.Kind) > 40 || len(r.Ref) > 200) {
			return "until_ref_change needs kind and ref."
		}
		a.Status, a.Read, a.Note = "deferred", true, note
		a.Defer, a.SnoozeUntil, a.WakeAtMS = &alertsapi.Defer{UntilMS: o.UntilMS, UntilRefChange: o.UntilRefChange}, nil, o.UntilMS
	case "settle":
		a.Status, a.Read, a.Note, a.ResolvedBy = "settled", true, note, "operator"
		a.Resolution = strings.TrimSpace(o.Resolution)
		if a.Resolution == "" {
			a.Resolution = "Handled"
		}
		if len(a.Resolution) > 100 {
			return "resolution is too long."
		}
		clear()
	case "reopen":
		a.Status, a.Read, a.Resolution, a.ResolvedBy = "active", false, "", ""
		clear()
	default:
		return "op must be one of " + strings.Join(alertsapi.Ops, ", ") + "."
	}
	return ""
}

func save(ctx context.Context, tx *sql.Tx, a alertsapi.Alert) error {
	var def any
	if a.Defer != nil {
		b, _ := json.Marshal(a.Defer)
		def = string(b)
	}
	_, err := tx.ExecContext(ctx, `UPDATE alerts SET seq=?, status=?, read=?, note=?, resolution=?, resolved_by=?, snooze_until_ms=?, wake_at_ms=?, defer_json=?, version=?, updated_ms=? WHERE alert_id=?`,
		a.Seq, a.Status, boolInt(a.Read), a.Note, a.Resolution, a.ResolvedBy, a.SnoozeUntil, a.WakeAtMS, def, a.Version, a.UpdatedMS, a.AlertID)
	return err
}

func load(ctx context.Context, tx *sql.Tx, id string) (alertsapi.Alert, error) {
	a, err := scanAlert(tx.QueryRowContext(ctx, `SELECT `+alertCols+` FROM alerts WHERE alert_id = ?`, id))
	if err == sql.ErrNoRows {
		return a, ErrNotFound
	}
	return a, err
}

// Apply performs one op under the version check and returns the updated alert. A stale version is a
// *ConflictError carrying the current alert; an invalid op is an *InvalidError.
func (s *Store) Apply(ctx context.Context, id string, o Op) (alertsapi.Alert, error) {
	if o.ExpectedVersion == nil {
		return alertsapi.Alert{}, &InvalidError{"expected_version is required."}
	}
	var out alertsapi.Alert
	var seq int64
	err := s.DB.Writer.Submit(ctx, "alert_op", func(ctx context.Context, tx *sql.Tx) error {
		a, err := load(ctx, tx, id)
		if err != nil {
			return err
		}
		if a.Version != *o.ExpectedVersion {
			return &ConflictError{Current: a}
		}
		now := s.nowMS()
		if msg := apply(&a, o, now); msg != "" {
			return &InvalidError{msg}
		}
		if seq, err = nextSeq(ctx, tx); err != nil {
			return err
		}
		a.Seq, a.Version, a.UpdatedMS = seq, a.Version+1, now
		out = a
		return save(ctx, tx, a)
	})
	if err == nil && s.OnChange != nil {
		s.OnChange(id, seq, "state")
	}
	return out, err
}

// BulkResult is the outcome of a bulk op.
type BulkResult struct {
	Updated, Skipped []string
}

// Bulk applies read or dismiss to the given ids, or to every active alert (scope all_active). A stale
// expectedSeq means the list changed since the operator looked: ErrSeqConflict, nothing applied.
func (s *Store) Bulk(ctx context.Context, op string, ids []string, scope string, expectedSeq *int64) (BulkResult, error) {
	if !slices.Contains(alertsapi.BulkOps, op) {
		return BulkResult{}, &InvalidError{"bulk op must be read or dismiss."}
	}
	if (len(ids) == 0) == (scope == "") || (scope != "" && scope != "all_active") || len(ids) > alertsapi.ListLimitMax {
		return BulkResult{}, &InvalidError{"Give ids (up to 200) or scope all_active."}
	}
	res := BulkResult{Updated: []string{}, Skipped: []string{}}
	var changes []struct {
		id  string
		seq int64
	}
	err := s.DB.Writer.Submit(ctx, "alert_bulk", func(ctx context.Context, tx *sql.Tx) error {
		if expectedSeq != nil {
			var cur int64
			if err := tx.QueryRowContext(ctx, `SELECT seq FROM alert_seq WHERE id = 1`).Scan(&cur); err != nil {
				return err
			}
			if cur != *expectedSeq {
				return ErrSeqConflict
			}
		}
		targets := ids
		if scope == "all_active" {
			targets = nil
			rows, err := tx.QueryContext(ctx, `SELECT alert_id FROM alerts WHERE status = 'active'`)
			if err != nil {
				return err
			}
			for rows.Next() {
				var id string
				if err := rows.Scan(&id); err != nil {
					rows.Close()
					return err
				}
				targets = append(targets, id)
			}
			rows.Close()
		}
		now := s.nowMS()
		for _, id := range targets {
			a, err := load(ctx, tx, id)
			if err == ErrNotFound || (err == nil && (op == "read" && a.Read || op == "dismiss" && a.Status != "active")) {
				res.Skipped = append(res.Skipped, id)
				continue
			}
			if err != nil {
				return err
			}
			apply(&a, Op{Op: op}, now)
			seq, err := nextSeq(ctx, tx)
			if err != nil {
				return err
			}
			a.Seq, a.Version, a.UpdatedMS = seq, a.Version+1, now
			if err := save(ctx, tx, a); err != nil {
				return err
			}
			res.Updated = append(res.Updated, id)
			changes = append(changes, struct {
				id  string
				seq int64
			}{id, seq})
		}
		return nil
	})
	if err == nil && s.OnChange != nil {
		for _, c := range changes {
			s.OnChange(c.id, c.seq, "state")
		}
	}
	return res, err
}

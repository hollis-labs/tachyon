// Package alertstore is the durable Alerts store (AL1): ingested events become alerts with stable ids,
// the operator's decisions (read, dismiss, snooze, defer, settle, reopen) are versioned, and every
// change bumps a monotonic sequence the stream (AL4) follows. Coalescing, escalation and wake-ups
// (AL3), humanised detail and approval linkage (AL5) and prefs semantics (AL6) build on it.
// Design: design_tachyon_alerts_backend_20261030, sections 3, 5 and 6.
package alertstore

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base32"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/hollis-labs/tachyon/internal/alertsapi"
	"github.com/hollis-labs/tachyon/internal/alertsource"
	"github.com/hollis-labs/tachyon/internal/alertsrules"
	"github.com/hollis-labs/tachyon/internal/localstore"
)

// Retention: terminal alerts 30 days, timeline events 7 days.
const (
	AlertRetention = 30 * 24 * time.Hour
	EventRetention = 7 * 24 * time.Hour
)

// Store implements alertsource.Sink and alertsource.CursorStore.
type Store struct {
	DB *localstore.Store
	// Pack returns the current rule pack; nil means no rules (nothing becomes an alert).
	Pack func() alertsrules.Pack
	Now  func() time.Time
	// Window is the coalesce window (the coalesce_window_sec pref); nil means DefaultCoalesceWindow, zero or less turns coalescing off.
	Window func(ctx context.Context) time.Duration
	// ApprovalState, if set, reports an approval item's current state for the detail's approval block.
	ApprovalState func(ctx context.Context, itemID string) string
	// OnChange, if set, is told after each committed change (the stream hook for AL4): the alert id, its
	// new sequence and what happened ("created", "updated", "state", "woke", "escalated", "removed"). updated, woke and
	// escalated are the changes that toast.
	OnChange func(alertID string, seq int64, change string)
}

func (s *Store) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func (s *Store) nowMS() int64 { return s.now().UnixMilli() }

var b32 = base32.NewEncoding("abcdefghijklmnopqrstuvwxyz234567").WithPadding(base32.NoPadding)

// AlertID is the stable id of an alert created from the event with this dedupe key.
func AlertID(dedupeKey string) string {
	sum := sha256.Sum256([]byte(dedupeKey))
	return "al_" + b32.EncodeToString(sum[:])[:26]
}

var (
	// ErrNotFound is returned for an unknown alert id.
	ErrNotFound = errors.New("alert not found")
	// ErrSeqConflict is returned by a bulk op whose expected_seq is stale.
	ErrSeqConflict = errors.New("alerts changed")
)

// InvalidError carries a message for a 400.
type InvalidError struct{ Msg string }

func (e *InvalidError) Error() string { return e.Msg }

// ConflictError is a stale expected_version; Current is the alert as it is now.
type ConflictError struct{ Current alertsapi.Alert }

func (e *ConflictError) Error() string { return "version conflict" }

func nextSeq(ctx context.Context, tx *sql.Tx) (int64, error) {
	if _, err := tx.ExecContext(ctx, `UPDATE alert_seq SET seq = seq + 1 WHERE id = 1`); err != nil {
		return 0, err
	}
	var n int64
	err := tx.QueryRowContext(ctx, `SELECT seq FROM alert_seq WHERE id = 1`).Scan(&n)
	return n, err
}

// Seq is the current change sequence.
func (s *Store) Seq(ctx context.Context) (int64, error) {
	var n int64
	err := s.DB.Reader.QueryRowContext(ctx, `SELECT seq FROM alert_seq WHERE id = 1`).Scan(&n)
	return n, err
}

// Cursor and SetCursor persist a source's resume cursor (alertsource.CursorStore).
func (s *Store) Cursor(source string) (string, error) {
	var c string
	err := s.DB.Reader.QueryRow(`SELECT cursor FROM alert_source_cursor WHERE source = ?`, source).Scan(&c)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return c, err
}

func (s *Store) SetCursor(source, cursor string) error {
	return s.DB.Writer.Submit(context.Background(), "alert_cursor", func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO alert_source_cursor(source,cursor,updated_ms) VALUES (?,?,?)
 ON CONFLICT(source) DO UPDATE SET cursor=excluded.cursor, updated_ms=excluded.updated_ms`, source, cursor, s.nowMS())
		return err
	})
}

// Ingest evaluates the event against the rule pack and, if it makes an alert, stores it. A redelivered
// event (same dedupe key) is ignored, so a reconnect never re-creates or re-toasts anything.
func (s *Store) Ingest(e alertsource.Event) error {
	if s.Pack == nil {
		return nil
	}
	if e.Kind == "alert.cleared" {
		_, err := s.SettleCleared(context.Background(), clearedKey(e))
		return err
	}
	res := s.Pack().Evaluate(e.RuleEvent())
	if res.Alert == nil {
		return nil
	}
	a := res.Alert
	dedupe := alertsource.DedupeKey(e)
	id := AlertID(dedupe)
	subjects := cleanSubjects(e.Subjects)
	sj, _ := json.Marshal(subjects)
	var primary any
	for _, sub := range subjects {
		if sub.URL != "" {
			p, _ := json.Marshal(alertsapi.Primary{Label: "Open " + sub.Kind, Href: sub.URL})
			primary = string(p)
			break
		}
	}
	var escalateAt any
	if a.Escalate != nil && a.Escalate.AfterSec > 0 {
		escalateAt = e.AtMS + int64(a.Escalate.AfterSec)*1000
	}
	ref := any(nil)
	if len(subjects) > 0 {
		r, _ := json.Marshal(subjects[0])
		ref = string(r)
	}
	window := s.window(context.Background())
	if a.WindowSec != nil { // a rule's own window beats the pref
		window = time.Duration(*a.WindowSec) * time.Second
	}
	coalesceKey := alertsource.Clean(a.CoalesceKey, 200)
	var seq int64
	var changes []change
	err := s.DB.Writer.Submit(context.Background(), "alert_ingest", func(ctx context.Context, tx *sql.Tx) error {
		changes = nil
		target := id
		foldID, foldSev, folded, err := fold(ctx, tx, coalesceKey, e.AtMS, window)
		if err != nil {
			return err
		}
		if folded {
			target = foldID
		}
		r, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO alert_events(alert_id, dedupe_key, at_ms, kind, label, ref_json, correlation_id, technical_json) VALUES (?,?,?,?,?,?,?,?)`,
			target, dedupe, e.AtMS, e.Kind, alertsource.Clean(e.Kind, 80), ref, alertsource.Clean(e.CorrelationID, 120), techJSON(e.Payload))
		if err != nil {
			return err
		}
		if n, _ := r.RowsAffected(); n == 0 {
			return nil // already ingested
		}
		if seq, err = nextSeq(ctx, tx); err != nil {
			return err
		}
		now := s.nowMS()
		if folded {
			if _, err = foldInto(ctx, tx, target, foldSev, a.Severity, a.NeedsYou, e.AtMS, now, seq); err != nil {
				return err
			}
			kind := "state"
			if sevRank(a.Severity) > sevRank(foldSev) {
				kind = "updated"
			}
			changes = append(changes, change{target, kind, seq})
		} else {
			_, err = tx.ExecContext(ctx, `INSERT INTO alerts(alert_id,seq,kind,rule_id,severity,severity_base,needs_you,title,summary,source,icon,status,read,count,coalesce_key,
 first_at_ms,last_at_ms,primary_json,subjects_json,escalate_at_ms,version,created_ms,updated_ms) VALUES (?,?,?,?,?,?,?,?,?,?,?,'active',0,1,?,?,?,?,?,?,1,?,?)`,
				id, seq, e.Kind, deref(res.RuleID), a.Severity, a.Severity, boolInt(a.NeedsYou),
				alertsource.Clean(a.Title, alertsapi.TitleMax), alertsource.Clean(a.Summary, alertsapi.SummaryMax),
				alertsource.Clean(e.Source, 64), a.Icon, coalesceKey,
				e.AtMS, e.AtMS, primary, string(sj), escalateAt, now, now)
			if err != nil {
				return err
			}
			changes = append(changes, change{id, "created", seq})
		}
		woken, err := s.wakeDeferredOnRef(ctx, tx, subjects, now)
		changes = append(changes, woken...)
		return err
	})
	if err == nil && s.OnChange != nil {
		for _, c := range changes {
			s.OnChange(c.id, c.seq, c.kind)
		}
	}
	return err
}

// Suppressed raises one "N events dropped from <source>" alert per source and minute.
func (s *Store) Suppressed(source string, n int) {
	if n <= 0 {
		return
	}
	at := s.nowMS()
	key := "suppressed|" + source + "|" + time.UnixMilli(at).UTC().Format("200601021504")
	id := AlertID(key)
	title := alertsource.Clean(itoa(n)+" events dropped from "+source, alertsapi.TitleMax)
	_ = s.DB.Writer.Submit(context.Background(), "alert_suppressed", func(ctx context.Context, tx *sql.Tx) error {
		r, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO alert_events(alert_id, dedupe_key, at_ms, kind, label) VALUES (?,?,?,?,?)`, id, key, at, "alert.suppressed", "Events dropped")
		if err != nil {
			return err
		}
		if c, _ := r.RowsAffected(); c == 0 {
			return nil
		}
		seq, err := nextSeq(ctx, tx)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO alerts(alert_id,seq,kind,rule_id,severity,severity_base,needs_you,title,summary,source,icon,status,read,count,coalesce_key,first_at_ms,last_at_ms,version,created_ms,updated_ms)
 VALUES (?,?,'alert.suppressed','','warning','warning',0,?,'The source exceeded its ingest rate limit.',?,'warning','active',0,1,?,?,?,1,?,?)`,
			id, seq, title, alertsource.Clean(source, 64), "suppressed|"+source, at, at, at, at)
		if err == nil && s.OnChange != nil {
			s.OnChange(id, seq, "created")
		}
		return err
	})
}

func cleanSubjects(in []alertsapi.Subject) []alertsapi.Subject {
	out := []alertsapi.Subject{}
	for _, s := range in {
		if len(out) >= 10 {
			break
		}
		c := alertsapi.Subject{Kind: alertsource.Clean(s.Kind, 40), Ref: alertsource.Clean(s.Ref, 200), Label: alertsource.Clean(s.Label, 120)}
		if c.Kind == "" || c.Ref == "" {
			continue
		}
		if s.URL != "" && alertsapi.SafeHref(s.URL) && len(s.URL) <= 500 {
			c.URL = s.URL
		}
		out = append(out, c)
	}
	return out
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

// Prune deletes terminal alerts older than 30 days and timeline events older than 7 days.
func (s *Store) Prune(ctx context.Context) error {
	now := s.now()
	return s.DB.Writer.Submit(ctx, "alert_prune", func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM alerts WHERE status IN ('dismissed','settled') AND updated_ms < ?`, now.Add(-AlertRetention).UnixMilli()); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM alert_events WHERE at_ms < ?`, now.Add(-EventRetention).UnixMilli())
		return err
	})
}

func lower(s string) string { return strings.ToLower(s) }

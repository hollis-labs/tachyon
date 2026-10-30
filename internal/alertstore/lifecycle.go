package alertstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"slices"
	"strconv"
	"time"

	"github.com/hollis-labs/tachyon/internal/alertsapi"
	"github.com/hollis-labs/tachyon/internal/alertsource"
)

// DefaultCoalesceWindow applies when the store has no Window func (the pref default).
const DefaultCoalesceWindow = 120 * time.Second

// SafetyTick is the longest the maintenance loop sleeps, whatever is due next.
const SafetyTick = 30 * time.Second

func (s *Store) window(ctx context.Context) time.Duration {
	if s.Window != nil {
		return s.Window(ctx)
	}
	return DefaultCoalesceWindow
}

func sevRank(sev string) int { return slices.Index(alertsapi.Severities, sev) }

// fold looks for an open alert (active or snoozed) with the same coalesce key whose last event is within
// the window. It returns its id, severity and unread flag. A repeat after dismiss or settle is not found,
// so it becomes a new alert.
func fold(ctx context.Context, tx *sql.Tx, key string, atMS int64, window time.Duration) (id, severity string, ok bool, err error) {
	if key == "" || window <= 0 {
		return "", "", false, nil
	}
	err = tx.QueryRowContext(ctx, `SELECT alert_id, severity FROM alerts WHERE coalesce_key = ? AND status IN ('active','snoozed') AND last_at_ms >= ? ORDER BY last_at_ms DESC LIMIT 1`,
		key, atMS-window.Milliseconds()).Scan(&id, &severity)
	if err == sql.ErrNoRows {
		return "", "", false, nil
	}
	return id, severity, err == nil, err
}

// foldInto adds the event to the open alert: count and last time move; a rise in severity makes it unread
// again (the stream reports it as updated with a toast), anything else leaves read as it was.
func foldInto(ctx context.Context, tx *sql.Tx, id, curSev, newSev string, needsYou bool, atMS, now, seq int64) (rose bool, err error) {
	rose = sevRank(newSev) > sevRank(curSev)
	sev := curSev
	if rose {
		sev = newSev
	}
	_, err = tx.ExecContext(ctx, `UPDATE alerts SET count = count + 1, last_at_ms = MAX(last_at_ms, ?), severity = ?, read = CASE WHEN ? THEN 0 ELSE read END,
 needs_you = MAX(needs_you, ?), seq = ?, version = version + 1, updated_ms = ? WHERE alert_id = ?`,
		atMS, sev, rose, boolInt(needsYou), seq, now, id)
	return rose, err
}

type change struct {
	id, kind string
	seq      int64
}

// wakeDeferredOnRef wakes deferred alerts waiting for a change to one of the event's subjects.
func (s *Store) wakeDeferredOnRef(ctx context.Context, tx *sql.Tx, subjects []alertsapi.Subject, now int64) ([]change, error) {
	if len(subjects) == 0 {
		return nil, nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT alert_id, defer_json FROM alerts WHERE status = 'deferred' AND defer_json IS NOT NULL`)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id, dj string
		if err := rows.Scan(&id, &dj); err != nil {
			rows.Close()
			return nil, err
		}
		var d alertsapi.Defer
		if json.Unmarshal([]byte(dj), &d) != nil || d.UntilRefChange == nil {
			continue
		}
		for _, sub := range subjects {
			if sub.Kind == d.UntilRefChange.Kind && sub.Ref == d.UntilRefChange.Ref {
				ids = append(ids, id)
				break
			}
		}
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	var out []change
	for _, id := range ids {
		c, err := s.wake(ctx, tx, id, "Event on the awaited item", now)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

// wake returns a snoozed or deferred alert to active and unread, with a timeline entry.
func (s *Store) wake(ctx context.Context, tx *sql.Tx, id, label string, now int64) (change, error) {
	seq, err := nextSeq(ctx, tx)
	if err != nil {
		return change{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE alerts SET status = 'active', read = 0, snooze_until_ms = NULL, wake_at_ms = NULL, defer_json = NULL,
 seq = ?, version = version + 1, updated_ms = ? WHERE alert_id = ?`, seq, now, id); err != nil {
		return change{}, err
	}
	_, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO alert_events(alert_id, dedupe_key, at_ms, kind, label) VALUES (?,?,?,?,?)`,
		id, "wake|"+id+"|"+strconv.FormatInt(seq, 10), now, "alert.woke", label)
	return change{id, "woke", seq}, err
}

// escalateTo is the severity the alert's rule escalates to, or "" when the rule is gone or has none.
func (s *Store) escalateTo(ruleID string) string {
	if s.Pack == nil {
		return ""
	}
	for _, r := range s.Pack().Rules {
		if r.ID == ruleID && r.Escalate != nil {
			return r.Escalate.To
		}
	}
	return ""
}

// Tick runs one maintenance pass: it wakes snoozed and deferred alerts whose time has come, and escalates
// unread active alerts past their deadline (severity up, needs you). Reading or handling an alert before
// its deadline cancels the escalation, because only active unread alerts are considered. The server clock
// decides; clients never evaluate expiry.
func (s *Store) Tick(ctx context.Context) (woke, escalated int, err error) {
	var changes []change
	err = s.DB.Writer.Submit(ctx, "alert_tick", func(ctx context.Context, tx *sql.Tx) error {
		changes = nil
		now := s.nowMS()
		rows, err := tx.QueryContext(ctx, `SELECT alert_id FROM alerts WHERE status IN ('snoozed','deferred') AND wake_at_ms IS NOT NULL AND wake_at_ms <= ?`, now)
		if err != nil {
			return err
		}
		var wake []string
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			wake = append(wake, id)
		}
		if err := rows.Close(); err != nil {
			return err
		}
		for _, id := range wake {
			c, err := s.wake(ctx, tx, id, "Snooze ended", now)
			if err != nil {
				return err
			}
			changes = append(changes, c)
		}
		rows, err = tx.QueryContext(ctx, `SELECT alert_id, rule_id, severity FROM alerts WHERE status = 'active' AND read = 0 AND escalate_at_ms IS NOT NULL AND escalated_at_ms IS NULL AND escalate_at_ms <= ?`, now)
		if err != nil {
			return err
		}
		type due struct{ id, rule, sev string }
		var ds []due
		for rows.Next() {
			var d due
			if err := rows.Scan(&d.id, &d.rule, &d.sev); err != nil {
				rows.Close()
				return err
			}
			ds = append(ds, d)
		}
		if err := rows.Close(); err != nil {
			return err
		}
		for _, d := range ds {
			to := s.escalateTo(d.rule)
			seq, err := nextSeq(ctx, tx)
			if err != nil {
				return err
			}
			if to == "" || sevRank(to) <= sevRank(d.sev) {
				// the rule changed or the alert already rose past the target: stop considering it, no re-toast
				if _, err = tx.ExecContext(ctx, `UPDATE alerts SET escalate_at_ms = NULL WHERE alert_id = ?`, d.id); err != nil {
					return err
				}
				continue
			}
			if _, err = tx.ExecContext(ctx, `UPDATE alerts SET severity = ?, needs_you = 1, escalated_at_ms = ?, seq = ?, version = version + 1, updated_ms = ? WHERE alert_id = ?`,
				to, now, seq, now, d.id); err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO alert_events(alert_id, dedupe_key, at_ms, kind, label) VALUES (?,?,?,?,?)`,
				d.id, "escalate|"+d.id, now, "alert.escalated", alertsource.Clean("Escalated to "+to, 80)); err != nil {
				return err
			}
			changes = append(changes, change{d.id, "escalated", seq})
		}
		return nil
	})
	if err != nil {
		return 0, 0, err
	}
	for _, c := range changes {
		if c.kind == "woke" {
			woke++
		} else {
			escalated++
		}
		if s.OnChange != nil {
			s.OnChange(c.id, c.seq, c.kind)
		}
	}
	return woke, escalated, nil
}

// NextDue is the earliest wake or escalation time, if any.
func (s *Store) NextDue(ctx context.Context) (time.Time, bool) {
	var ms sql.NullInt64
	err := s.DB.Reader.QueryRowContext(ctx, `SELECT MIN(t) FROM (
 SELECT MIN(wake_at_ms) AS t FROM alerts WHERE status IN ('snoozed','deferred') AND wake_at_ms IS NOT NULL
 UNION ALL
 SELECT MIN(escalate_at_ms) FROM alerts WHERE status = 'active' AND read = 0 AND escalate_at_ms IS NOT NULL AND escalated_at_ms IS NULL)`).Scan(&ms)
	if err != nil || !ms.Valid {
		return time.Time{}, false
	}
	return time.UnixMilli(ms.Int64), true
}

// Maintain runs Tick until ctx ends: it sleeps until the next due time, never longer than SafetyTick, so
// a wake lands on time even when nothing else prompts a pass. Errors are reported through onErr and the
// loop keeps going.
func (s *Store) Maintain(ctx context.Context, onErr func(error)) {
	for {
		if _, _, err := s.Tick(ctx); err != nil && ctx.Err() == nil && onErr != nil {
			onErr(err)
		}
		wait := SafetyTick
		if due, ok := s.NextDue(ctx); ok {
			if d := due.Sub(s.now()); d < wait {
				wait = d
			}
		}
		if wait < 250*time.Millisecond {
			wait = 250 * time.Millisecond
		}
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C:
		}
	}
}

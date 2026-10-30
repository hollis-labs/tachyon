package alertstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/hollis-labs/tachyon/internal/alertsapi"
)

const alertCols = `alert_id, seq, kind, rule_id, severity, severity_base, needs_you, title, summary, source, icon, status, read, count, coalesce_key,
 first_at_ms, last_at_ms, snooze_until_ms, wake_at_ms, defer_json, note, resolution, resolved_by, primary_json, subjects_json,
 escalate_at_ms, escalated_at_ms, version, created_ms, updated_ms`

type scanner interface{ Scan(...any) error }

func scanAlert(r scanner) (alertsapi.Alert, error) {
	var a alertsapi.Alert
	var needs, read int
	var snooze, wake, esc, escd sql.NullInt64
	var def, prim sql.NullString
	var subj string
	if err := r.Scan(&a.AlertID, &a.Seq, &a.Kind, &a.RuleID, &a.Severity, &a.SeverityBase, &needs, &a.Title, &a.Summary, &a.Source, &a.Icon, &a.Status,
		&read, &a.Count, &a.CoalesceKey, &a.FirstAtMS, &a.LastAtMS, &snooze, &wake, &def, &a.Note, &a.Resolution, &a.ResolvedBy, &prim, &subj,
		&esc, &escd, &a.Version, &a.CreatedMS, &a.UpdatedMS); err != nil {
		return a, err
	}
	a.NeedsYou, a.Read = needs == 1, read == 1
	ptr := func(n sql.NullInt64) *int64 {
		if !n.Valid {
			return nil
		}
		v := n.Int64
		return &v
	}
	a.SnoozeUntil, a.WakeAtMS, a.EscalateAtMS, a.EscalatedAt = ptr(snooze), ptr(wake), ptr(esc), ptr(escd)
	if def.Valid {
		var d alertsapi.Defer
		if json.Unmarshal([]byte(def.String), &d) == nil {
			a.Defer = &d
		}
	}
	if prim.Valid {
		var p alertsapi.Primary
		if json.Unmarshal([]byte(prim.String), &p) == nil {
			a.Primary = &p
		}
	}
	if json.Unmarshal([]byte(subj), &a.Subjects) != nil || a.Subjects == nil {
		a.Subjects = []alertsapi.Subject{}
	}
	alertsapi.Decorate(&a)
	return a, nil
}

// Get returns one alert.
func (s *Store) Get(ctx context.Context, id string) (alertsapi.Alert, error) {
	a, err := scanAlert(s.DB.Reader.QueryRowContext(ctx, `SELECT `+alertCols+` FROM alerts WHERE alert_id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return a, ErrNotFound
	}
	return a, err
}

// ListQuery filters GET /. NeedsYou is nil (any), or a pointer to the wanted value.
type ListQuery struct {
	View        string // active | history
	NeedsYou    *bool
	MinSeverity string
	Source      string
	Q           string
	Limit       int
	Cursor      string
}

// cursor is "<last_at_ms>:<seq>" of the last row returned; rows are newest first by (last_at_ms, seq).
func parseCursor(c string) (int64, int64, bool) {
	a, b, ok := strings.Cut(c, ":")
	if !ok {
		return 0, 0, false
	}
	x, e1 := strconv.ParseInt(a, 10, 64)
	y, e2 := strconv.ParseInt(b, 10, 64)
	return x, y, e1 == nil && e2 == nil && x >= 0 && y >= 0
}

// List returns a page and the next cursor ("" when exhausted). Active holds status active only;
// history holds the other statuses, 30 days.
func (s *Store) List(ctx context.Context, q ListQuery) ([]alertsapi.Alert, string, error) {
	if q.Limit <= 0 {
		q.Limit = alertsapi.ListLimitDef
	}
	where, args := []string{}, []any{}
	if q.View == "history" {
		where = append(where, `status <> 'active'`, `updated_ms >= ?`)
		args = append(args, s.now().Add(-AlertRetention).UnixMilli())
	} else {
		where = append(where, `status = 'active'`)
	}
	if q.NeedsYou != nil {
		where = append(where, `needs_you = ?`)
		args = append(args, boolInt(*q.NeedsYou))
	}
	if q.MinSeverity != "" {
		var ok []string
		for i, sv := range alertsapi.Severities {
			if i >= sevIndex(q.MinSeverity) {
				ok = append(ok, "'"+sv+"'")
			}
		}
		where = append(where, `severity IN (`+strings.Join(ok, ",")+`)`)
	}
	if q.Source != "" {
		where = append(where, `lower(source) = ?`)
		args = append(args, lower(q.Source))
	}
	if q.Q != "" {
		where = append(where, `lower(title || ' ' || summary || ' ' || source) LIKE ? ESCAPE '\'`)
		args = append(args, "%"+likeEscape(lower(q.Q))+"%")
	}
	if q.Cursor != "" {
		at, seq, ok := parseCursor(q.Cursor)
		if !ok {
			return nil, "", &InvalidError{"cursor is not valid."}
		}
		where = append(where, `(last_at_ms < ? OR (last_at_ms = ? AND seq < ?))`)
		args = append(args, at, at, seq)
	}
	args = append(args, q.Limit+1)
	rows, err := s.DB.Reader.QueryContext(ctx, `SELECT `+alertCols+` FROM alerts WHERE `+strings.Join(where, " AND ")+` ORDER BY last_at_ms DESC, seq DESC LIMIT ?`, args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	out := []alertsapi.Alert{}
	for rows.Next() {
		a, err := scanAlert(rows)
		if err != nil {
			return nil, "", err
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(out) > q.Limit {
		out = out[:q.Limit]
		last := out[len(out)-1]
		next = fmt.Sprintf("%d:%d", last.LastAtMS, last.Seq)
	}
	return out, next, nil
}

func sevIndex(s string) int {
	for i, v := range alertsapi.Severities {
		if v == s {
			return i
		}
	}
	return 0
}

func likeEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// Counts drive the bell badge: unread and needs-you count active alerts only.
func (s *Store) Counts(ctx context.Context) (alertsapi.Counts, error) {
	c := alertsapi.Counts{BySeverity: map[string]int{}}
	for _, sv := range alertsapi.Severities {
		c.BySeverity[sv] = 0
	}
	rows, err := s.DB.Reader.QueryContext(ctx, `SELECT severity, read, needs_you, COUNT(*) FROM alerts WHERE status = 'active' GROUP BY severity, read, needs_you`)
	if err != nil {
		return c, err
	}
	defer rows.Close()
	for rows.Next() {
		var sev string
		var read, needs, n int
		if err := rows.Scan(&sev, &read, &needs, &n); err != nil {
			return c, err
		}
		c.Active += n
		c.BySeverity[sev] += n
		if read == 0 {
			c.Unread += n
			if needs == 1 {
				c.NeedsYou += n
			}
		}
	}
	if err := rows.Err(); err != nil {
		return c, err
	}
	c.Seq, err = s.Seq(ctx)
	return c, err
}

// TimelineEntry is one related event.
type TimelineEntry struct {
	AtMS  int64           `json:"at_ms"`
	Label string          `json:"label"`
	Ref   json.RawMessage `json:"ref"`
}

// Detail is GET /{id}: the alert, its timeline, linked subjects, allowlisted technical fields from the latest
// event, actions, and the approval block when the alert is about a queue item (the drawer decides through the
// Approvals endpoints, never here).
type Detail struct {
	Alert     alertsapi.Alert     `json:"alert"`
	Timeline  []TimelineEntry     `json:"timeline"`
	Linked    []alertsapi.Subject `json:"linked"`
	Technical map[string]any      `json:"technical"`
	Actions   []map[string]any    `json:"actions"`
	Approval  any                 `json:"approval"`
	ServerNow int64               `json:"server_now_ms"`
}

func (s *Store) Detail(ctx context.Context, id string) (Detail, error) {
	a, err := s.Get(ctx, id)
	if err != nil {
		return Detail{}, err
	}
	d := Detail{Alert: a, Linked: a.Subjects, Technical: map[string]any{"fields": []any{}}, Actions: []map[string]any{}, ServerNow: s.nowMS()}
	rows, err := s.DB.Reader.QueryContext(ctx, `SELECT at_ms, label, ref_json FROM alert_events WHERE alert_id = ? ORDER BY at_ms DESC, event_id DESC LIMIT ?`, id, alertsapi.TimelineMax)
	if err != nil {
		return d, err
	}
	defer rows.Close()
	d.Timeline = []TimelineEntry{}
	for rows.Next() {
		var e TimelineEntry
		var ref sql.NullString
		if err := rows.Scan(&e.AtMS, &e.Label, &ref); err != nil {
			return d, err
		}
		e.Ref = json.RawMessage("null")
		if ref.Valid {
			e.Ref = json.RawMessage(ref.String)
		}
		d.Timeline = append(d.Timeline, e)
	}
	if len(d.Timeline) == 0 {
		d.Timeline = []TimelineEntry{{AtMS: a.LastAtMS, Label: "This alert", Ref: json.RawMessage("null")}}
	}
	if a.Primary != nil {
		d.Actions = append(d.Actions, map[string]any{"id": "open", "label": a.Primary.Label, "kind": "link"})
	}
	if err := rows.Err(); err != nil {
		return d, err
	}
	if d.Approval = s.approvalBlock(ctx, a); d.Approval != nil {
		d.Actions = append(d.Actions, map[string]any{"id": "approval", "label": "Review approval", "kind": "approval"})
	}
	var tj sql.NullString
	if err := s.DB.Reader.QueryRowContext(ctx, `SELECT technical_json FROM alert_events WHERE alert_id = ? ORDER BY at_ms DESC, event_id DESC LIMIT 1`, id).Scan(&tj); err == nil && tj.Valid {
		var fields []map[string]string
		if json.Unmarshal([]byte(tj.String), &fields) == nil && len(fields) > 0 {
			d.Technical = map[string]any{"fields": fields}
		}
	}
	return d, nil
}

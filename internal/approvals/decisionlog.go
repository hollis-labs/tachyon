package approvals

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/hollis-labs/libs/mesh/core/hitl"
)

// Event is one row of the append-only decision log. Terminal events (resolved,
// expired, withdrawn) are written in the same transaction as the record change and
// are reconstructible from the records (BackfillLog); the rest (presented,
// deferred, undeferred) exist only here.
type Event struct {
	Seq        int64
	EventID    string
	TS         time.Time
	ItemID     string
	Kind       string // presented|deferred|undeferred|resolved|batch_resolved|expired|withdrawn|confirmation_failed|access_denied
	Status     string // approved|denied|answered|expired|withdrawn for terminal kinds
	ActorRef   string
	Assurance  string
	Decision   string
	Note       string
	ReasonCode string
	BatchID    string
	Revision   int64
	Extra      map[string]any
}

const maxNote = 2000

func clip(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

func newEventID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return "evt_" + hex.EncodeToString(b[:])
}

// insertEvent appends an event. An existing event_id is ignored, which makes
// deterministic ids (terminal events, one presentation per session) idempotent.
func insertEvent(ctx context.Context, tx *sql.Tx, e Event) error {
	if e.EventID == "" {
		e.EventID = newEventID()
	}
	if e.TS.IsZero() {
		e.TS = time.Now()
	}
	extra := ""
	if len(e.Extra) > 0 {
		b, _ := json.Marshal(e.Extra)
		extra = string(b)
	}
	_, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO approval_decision_log(event_id,ts_ns,item_id,kind,status,actor_ref,assurance,decision,note,reason_code,batch_id,revision,extra) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		e.EventID, e.TS.UnixNano(), e.ItemID, e.Kind, e.Status, e.ActorRef, e.Assurance, e.Decision, clip(e.Note, maxNote), e.ReasonCode, e.BatchID, e.Revision, extra)
	return err
}

// terminalEvent derives the log event of a terminal record. Failed and superseded
// items have no decision to show and yield ok=false.
func terminalEvent(rec hitl.Record) (Event, bool) {
	e := Event{EventID: "evt_" + rec.ItemID + "_terminal", ItemID: rec.ItemID, Revision: rec.Revision}
	switch o := rec.Outcome.(type) {
	case hitl.Resolved:
		r := o.Resolution
		e.Kind, e.TS, e.Revision = "resolved", r.ResolvedAt, o.InteractionRevision
		e.Decision, e.Status = r.Response.Decision, statusOf(rec)
		e.Note = r.Response.Note
		if e.Note == "" {
			e.Note = r.Response.Reply
		}
		if e.Note == "" {
			_ = json.Unmarshal(r.Response.Extra["note"], &e.Note)
		}
		e.ActorRef, e.Assurance = r.Participant.PrincipalRef, r.Participant.Assurance
		if e.Status == "denied" {
			e.ReasonCode = "denied"
		}
	case hitl.Expired:
		e.Kind, e.Status, e.TS, e.Revision, e.ReasonCode = "expired", "expired", o.TerminatedAt, o.InteractionRevision, "deadline"
	case hitl.Canceled:
		e.Kind, e.Status, e.TS, e.Revision, e.ReasonCode, e.Note = "withdrawn", "withdrawn", o.TerminatedAt, o.InteractionRevision, string(o.Cause), o.Reason
	default:
		return Event{}, false
	}
	return e, true
}

// logTerminalTx records the terminal event of next and drops any deferral, inside the record's transaction.
func logTerminalTx(ctx context.Context, tx *sql.Tx, next hitl.Record) {
	if !next.State.IsTerminal() {
		return
	}
	if e, ok := terminalEvent(next); ok {
		_ = insertEvent(ctx, tx, e)
	}
	_, _ = tx.ExecContext(ctx, `DELETE FROM approval_defers WHERE item_id=?`, next.ItemID)
	_, _ = tx.ExecContext(ctx, `UPDATE approval_item_index SET deferred_until_ns=NULL WHERE item_id=?`, next.ItemID)
}

// LogEvent appends an event outside a record change (presented, access denied, ...).
func (s *Store) LogEvent(ctx context.Context, e Event) error {
	return s.DB.Writer.Submit(ctx, "approval_log_event", func(ctx context.Context, tx *sql.Tx) error { return insertEvent(ctx, tx, e) })
}

// BackfillLog adds the terminal event of any finished record that has none, so the
// log can be repaired from the records. Idempotent.
func (s *Store) BackfillLog(ctx context.Context) (int, error) {
	rows, err := s.DB.Reader.QueryContext(ctx, `SELECT i.record FROM approval_items i WHERE i.terminal=1 AND NOT EXISTS (SELECT 1 FROM approval_decision_log l WHERE l.event_id='evt_'||i.id||'_terminal') LIMIT 500`)
	if err != nil {
		return 0, err
	}
	var recs []hitl.Record
	for rows.Next() {
		rec, err := readRecord(rows)
		if err != nil {
			rows.Close()
			return 0, err
		}
		recs = append(recs, rec)
	}
	rows.Close()
	n := 0
	err = s.DB.Writer.Submit(ctx, "approval_log_backfill", func(ctx context.Context, tx *sql.Tx) error {
		for _, rec := range recs {
			if e, ok := terminalEvent(rec); ok {
				if err := insertEvent(ctx, tx, e); err != nil {
					return err
				}
				n++
			}
		}
		return nil
	})
	return n, err
}

// EventFilter selects history rows. Zero values mean no filter.
type EventFilter struct {
	Status, Requester, Q string
	Kinds                []string
	From, To             time.Time
	After                int64 // cursor: the seq of the last row returned
	Limit                int
}

// Row of the history timeline, with the item's mini description.
type TimelineRow struct {
	Event
	Title, Group, Risk string
	ReqID, ReqLabel    string // the item's requester, from the index ("" when the item is not indexed)
	ReqKind            string
}

// Facet is one row of a History facet menu.
type Facet struct {
	ID, Label, Status string
	Count             int
}

// Timeline returns events newest first with the item's title, group and risk.
func (s *Store) Timeline(ctx context.Context, f EventFilter) ([]TimelineRow, int64, error) {
	limit := f.Limit
	if limit <= 0 || limit > 100 {
		limit = 25
	}
	where, args := timelineWhere(f, "")
	if f.After > 0 {
		where, args = append(where, "l.seq<?"), append(args, f.After)
	}
	rows, err := s.DB.Reader.QueryContext(ctx, `SELECT l.seq,l.event_id,l.ts_ns,l.item_id,l.kind,l.status,l.actor_ref,l.assurance,l.decision,l.note,l.reason_code,l.batch_id,l.revision,l.extra,COALESCE(x.title,''),COALESCE(x.grp,''),COALESCE(x.risk,''),COALESCE(x.requester_id,''),COALESCE(x.requester_label,''),COALESCE(x.requester_kind,'')
FROM approval_decision_log l LEFT JOIN approval_item_index x ON x.item_id=l.item_id WHERE `+strings.Join(where, " AND ")+` ORDER BY l.seq DESC LIMIT ?`, append(args, limit+1)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out, err := scanEvents(rows)
	if err != nil {
		return nil, 0, err
	}
	var next int64
	if len(out) > limit {
		out = out[:limit]
		next = out[len(out)-1].Seq
	}
	return out, next, nil
}

// timelineWhere builds the filter shared by the page and its facets. skip names a filter to leave out
// ("status" or "requester"), which is how a facet menu counts over the other filters only.
func timelineWhere(f EventFilter, skip string) ([]string, []any) {
	where, args := []string{"1=1"}, []any{}
	if len(f.Kinds) > 0 {
		where = append(where, "l.kind IN ("+strings.TrimSuffix(strings.Repeat("?,", len(f.Kinds)), ",")+")")
		for _, k := range f.Kinds {
			args = append(args, k)
		}
	}
	if f.Status != "" && skip != "status" {
		where, args = append(where, "l.status=?"), append(args, f.Status)
	}
	if f.Requester != "" && skip != "requester" {
		where, args = append(where, "x.requester_id=?"), append(args, f.Requester)
	}
	if q := strings.ToLower(strings.TrimSpace(f.Q)); q != "" {
		where, args = append(where, "(instr(x.search,?)>0 OR instr(lower(l.note),?)>0)"), append(args, q, q)
	}
	if !f.From.IsZero() {
		where, args = append(where, "l.ts_ns>=?"), append(args, f.From.UnixNano())
	}
	if !f.To.IsZero() {
		where, args = append(where, "l.ts_ns<=?"), append(args, f.To.UnixNano())
	}
	return where, args
}

// ItemEvents returns one item's events oldest first.
func (s *Store) ItemEvents(ctx context.Context, id string) ([]TimelineRow, error) {
	rows, err := s.DB.Reader.QueryContext(ctx, `SELECT l.seq,l.event_id,l.ts_ns,l.item_id,l.kind,l.status,l.actor_ref,l.assurance,l.decision,l.note,l.reason_code,l.batch_id,l.revision,l.extra,'','','','','',''
FROM approval_decision_log l WHERE l.item_id=? ORDER BY l.seq`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanEvents(rows)
}

func scanEvents(rows *sql.Rows) ([]TimelineRow, error) {
	var out []TimelineRow
	for rows.Next() {
		var r TimelineRow
		var ts int64
		var extra string
		if err := rows.Scan(&r.Seq, &r.EventID, &ts, &r.ItemID, &r.Kind, &r.Status, &r.ActorRef, &r.Assurance, &r.Decision, &r.Note, &r.ReasonCode, &r.BatchID, &r.Revision, &extra, &r.Title, &r.Group, &r.Risk, &r.ReqID, &r.ReqLabel, &r.ReqKind); err != nil {
			return nil, err
		}
		r.TS = time.Unix(0, ts).UTC()
		if extra != "" {
			_ = json.Unmarshal([]byte(extra), &r.Extra)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Defer errors the HTTP layer maps to responses.
var (
	ErrDeferStale    = errors.New("approvals: item revision changed")
	ErrDeferTerminal = errors.New("approvals: item is already decided")
)

// Defer snoozes a pending item until `until` (already clamped by the caller to its
// expiry). The record is untouched: the overlay only changes ordering and batch
// eligibility. It is logged in the same transaction.
func (s *Store) Defer(ctx context.Context, id string, expected int64, until time.Time, reason string, actor, assurance string) (hitl.Record, error) {
	var rec hitl.Record
	err := s.DB.Writer.Submit(ctx, "approval_defer", func(ctx context.Context, tx *sql.Tx) error {
		var err error
		if rec, err = readRecord(tx.QueryRowContext(ctx, "SELECT record FROM approval_items WHERE id=?", id)); err != nil {
			return err
		}
		if rec.State.IsTerminal() {
			return ErrDeferTerminal
		}
		if rec.Revision != expected {
			return ErrDeferStale
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO approval_defers(item_id,until_ns,reason,deferred_by,created_ns) VALUES(?,?,?,?,?) ON CONFLICT(item_id) DO UPDATE SET until_ns=excluded.until_ns,reason=excluded.reason,deferred_by=excluded.deferred_by,created_ns=excluded.created_ns`,
			id, until.UnixNano(), clip(reason, 280), actor, time.Now().UnixNano()); err != nil {
			return err
		}
		_, _ = tx.ExecContext(ctx, `UPDATE approval_item_index SET deferred_until_ns=? WHERE item_id=?`, until.UnixNano(), id)
		return insertEvent(ctx, tx, Event{ItemID: id, Kind: "deferred", ActorRef: actor, Assurance: assurance, Note: reason, Revision: rec.Revision, Extra: map[string]any{"until": until.UTC().Format(time.RFC3339)}})
	})
	if err == nil {
		Changes(s.DB).Publish(Change{Type: "item.deferred", ItemID: id, Revision: rec.Revision, State: string(rec.State), Extra: map[string]any{"deferred_until": until.UTC().Format(time.RFC3339)}})
	}
	return rec, err
}

// Undefer removes a snooze. Removing none is not an error.
func (s *Store) Undefer(ctx context.Context, id string, actor, assurance string) (hitl.Record, error) {
	var rec hitl.Record
	removed := false
	err := s.DB.Writer.Submit(ctx, "approval_undefer", func(ctx context.Context, tx *sql.Tx) error {
		var err error
		if rec, err = readRecord(tx.QueryRowContext(ctx, "SELECT record FROM approval_items WHERE id=?", id)); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `DELETE FROM approval_defers WHERE item_id=?`, id)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		if removed = n > 0; !removed {
			return nil
		}
		_, _ = tx.ExecContext(ctx, `UPDATE approval_item_index SET deferred_until_ns=NULL WHERE item_id=?`, id)
		return insertEvent(ctx, tx, Event{ItemID: id, Kind: "undeferred", ActorRef: actor, Assurance: assurance, Revision: rec.Revision})
	})
	if err == nil && removed {
		Changes(s.DB).Publish(Change{Type: "item.undeferred", ItemID: id, Revision: rec.Revision, State: string(rec.State)})
	}
	return rec, err
}

// DeferredUntil returns the live snooze end of an item, or nil.
func (s *Store) DeferredUntil(ctx context.Context, id string, now time.Time) *time.Time {
	var ns sql.NullInt64
	if s.DB.Reader.QueryRowContext(ctx, `SELECT until_ns FROM approval_defers WHERE item_id=?`, id).Scan(&ns) != nil || !ns.Valid || ns.Int64 <= now.UnixNano() {
		return nil
	}
	t := time.Unix(0, ns.Int64).UTC()
	return &t
}

// TimelineFacets counts the requesters and statuses of the events the filter selects, each over every
// filter but its own, so choosing one value does not hide the others. The cursor does not apply.
func (s *Store) TimelineFacets(ctx context.Context, f EventFilter) (requesters, statuses []Facet, err error) {
	count := func(skip, cols, group string, scan func(*sql.Rows) (Facet, error), extra string) ([]Facet, error) {
		where, args := timelineWhere(f, skip)
		if extra != "" {
			where = append(where, extra)
		}
		rows, err := s.DB.Reader.QueryContext(ctx, `SELECT `+cols+`, COUNT(*) FROM approval_decision_log l LEFT JOIN approval_item_index x ON x.item_id=l.item_id WHERE `+strings.Join(where, " AND ")+` GROUP BY `+group+` ORDER BY COUNT(*) DESC, 1`, args...)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		out := []Facet{}
		for rows.Next() {
			fc, err := scan(rows)
			if err != nil {
				return nil, err
			}
			out = append(out, fc)
		}
		return out, rows.Err()
	}
	requesters, err = count("requester", "x.requester_id, x.requester_label", "x.requester_id, x.requester_label", func(r *sql.Rows) (Facet, error) {
		var fc Facet
		return fc, r.Scan(&fc.ID, &fc.Label, &fc.Count)
	}, "x.requester_id IS NOT NULL")
	if err != nil {
		return nil, nil, err
	}
	statuses, err = count("status", "l.status", "l.status", func(r *sql.Rows) (Facet, error) {
		var fc Facet
		return fc, r.Scan(&fc.Status, &fc.Count)
	}, "")
	return requesters, statuses, err
}

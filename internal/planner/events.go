package planner

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hollis-labs/tachyon/internal/localstore"
	"github.com/hollis-labs/tachyon/internal/plannerapi"
)

// Event is one timeline entry. Appending is idempotent by (Source, SourceEventID).
type Event struct {
	ThemeID, SubjectRef, Kind, Text, Arrow, Source, SourceEventID, Status string
	AtMS                                                                  int64
	Refs                                                                  []Item
	Delta                                                                 map[string]int
	Weight                                                                float64
}

var zeroDelta = func() map[string]int { return map[string]int{"done": 0, "doing": 0, "review": 0, "blocked": 0} }

// Events stores the timeline and the SSE ring.
type Events struct{ DB *localstore.Store }

const (
	streamKeep = 2000
	stateTail  = "revision_cursor"
)

func streamAppend(ctx context.Context, tx *sql.Tx, event string, data any) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO planner_stream(event,data_json,at_ms) VALUES(?,?,?)`, event, string(encode(data)), time.Now().UnixMilli())
	return err
}

func stateGet(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, k string) (string, bool) {
	var v string
	if err := q.QueryRowContext(ctx, `SELECT v FROM planner_ingest_state WHERE k=?`, k).Scan(&v); err != nil {
		return "", false
	}
	return v, true
}

func statePut(ctx context.Context, tx *sql.Tx, k, v string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO planner_ingest_state(k,v,updated_ms) VALUES(?,?,?) ON CONFLICT(k) DO UPDATE SET v=excluded.v,updated_ms=excluded.updated_ms`, k, v, time.Now().UnixMilli())
	return err
}

// Append stores events and announces them on the stream; it reports how many were new.
func (e Events) Append(ctx context.Context, evs ...Event) (int, error) {
	added := 0
	err := e.DB.Writer.Submit(ctx, "planner_events", func(ctx context.Context, tx *sql.Tx) error {
		for _, ev := range evs {
			if ev.Arrow == "" {
				ev.Arrow = "flat"
			}
			if ev.Delta == nil {
				ev.Delta = zeroDelta()
			}
			if ev.Refs == nil {
				ev.Refs = []Item{}
			}
			res, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO planner_events(event_id,at_ms,theme_id,subject_ref,kind,text,refs_json,delta_json,arrow,weight,source,source_event_id) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
				uuid.NewString(), ev.AtMS, ev.ThemeID, ev.SubjectRef, ev.Kind, ev.Text, string(encode(ev.Refs)), string(encode(ev.Delta)), ev.Arrow, ev.Weight, ev.Source, ev.SourceEventID)
			if err != nil {
				return err
			}
			if n, _ := res.RowsAffected(); n == 0 {
				continue
			}
			seq, _ := res.LastInsertId()
			added++
			if err := streamAppend(ctx, tx, "planner.event", Item{"theme_id": ev.ThemeID, "seq": seq}); err != nil {
				return err
			}
			if ev.Kind == "task" || ev.Kind == "blocker" {
				for _, r := range ev.Refs {
					if r["kind"] == "torque" {
						if err := streamAppend(ctx, tx, "planner.task", Item{"task": r["ref"], "status": ev.Status}); err != nil {
							return err
						}
					}
				}
			}
		}
		return nil
	})
	return added, err
}

// Timeline pages events newest first. subjects limits to those subject refs when set; otherwise
// the theme's events are returned.
func (e Events) Timeline(ctx context.Context, themeID string, subjects []string, facet string, before int64, limit int) ([]Item, any, error) {
	if limit < 1 {
		limit = plannerapi.DefaultTimeline
	}
	if limit > plannerapi.MaxTimeline {
		limit = plannerapi.MaxTimeline
	}
	kinds := map[string]string{"tasks": "task", "prs": "pr", "decisions": "decision", "blockers": "blocker", "comments": "comment", "workstreams": "workstream"}
	q := `SELECT seq,at_ms,kind,text,refs_json,delta_json,arrow FROM planner_events WHERE `
	args := []any{}
	if len(subjects) > 0 {
		q += `subject_ref IN (` + strings.TrimSuffix(strings.Repeat("?,", len(subjects)), ",") + `)`
		for _, s := range subjects {
			args = append(args, s)
		}
	} else {
		q += `theme_id=?`
		args = append(args, themeID)
	}
	if k, ok := kinds[facet]; ok {
		q += ` AND kind=?`
		args = append(args, k)
	}
	if before > 0 {
		q += ` AND seq<?`
		args = append(args, before)
	}
	q += ` ORDER BY seq DESC LIMIT ?`
	args = append(args, limit+1)
	rows, err := e.DB.Reader.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	out := []Item{}
	for rows.Next() {
		var seq, at int64
		var kind, text, refs, delta, arrow string
		if err := rows.Scan(&seq, &at, &kind, &text, &refs, &delta, &arrow); err != nil {
			return nil, nil, err
		}
		var r []any
		var d map[string]any
		_ = json.Unmarshal([]byte(refs), &r)
		_ = json.Unmarshal([]byte(delta), &d)
		out = append(out, Item{"seq": seq, "at": time.UnixMilli(at).UTC().Format(time.RFC3339), "kind": kind, "text": text, "refs": r, "delta": d, "arrow": arrow})
	}
	var next any
	if len(out) > limit {
		out = out[:limit]
		next = out[limit-1]["seq"]
	}
	return out, next, rows.Err()
}

// Prune drops events past retention and trims the stream ring.
func (e Events) Prune(ctx context.Context, retentionDays int) error {
	return e.DB.Writer.Submit(ctx, "planner_events_prune", func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM planner_events WHERE at_ms<?`, time.Now().AddDate(0, 0, -retentionDays).UnixMilli()); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM planner_stream WHERE seq<=(SELECT coalesce(max(seq),0) FROM planner_stream)-?`, streamKeep)
		return err
	})
}

// streamBounds returns the lowest retained and the highest stream seq.
func (e Events) streamBounds(ctx context.Context) (lo, hi int64, err error) {
	err = e.DB.Reader.QueryRowContext(ctx, `SELECT coalesce(min(seq),0), coalesce(max(seq),0) FROM planner_stream`).Scan(&lo, &hi)
	return
}

type streamRow struct {
	Seq   int64
	Event string
	Data  string
}

func (e Events) streamAfter(ctx context.Context, after int64) ([]streamRow, error) {
	rows, err := e.DB.Reader.QueryContext(ctx, `SELECT seq,event,data_json FROM planner_stream WHERE seq>? ORDER BY seq LIMIT 500`, after)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []streamRow
	for rows.Next() {
		var r streamRow
		if err := rows.Scan(&r.Seq, &r.Event, &r.Data); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

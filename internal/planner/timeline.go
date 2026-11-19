package planner

import (
	"context"
	"net/url"
	"slices"
	"strconv"
	"time"

	"github.com/hollis-labs/tachyon/internal/objects"
	"github.com/hollis-labs/tachyon/internal/plannerapi"
)

// Timeline answers GET themes/{id}/timeline and workstreams/{id}/timeline: milestones (themes) and
// events newest first, paged by before_seq. Momentum and the progress series are PL5.
func (h *HTTP) Timeline(ctx context.Context, db, id string, q url.Values) (Item, error) {
	facet := q.Get("facet")
	if facet == "" {
		facet = "all"
	}
	if !slices.Contains(plannerapi.Facets, facet) {
		return nil, objects.ErrInvalid
	}
	var limit int
	var before int64
	var err error
	if v := q.Get("limit"); v != "" {
		if limit, err = strconv.Atoi(v); err != nil || limit < 1 || limit > plannerapi.MaxTimeline {
			return nil, objects.ErrInvalid
		}
	}
	if v := q.Get("before_seq"); v != "" {
		if before, err = strconv.ParseInt(v, 10, 64); err != nil || before < 1 {
			return nil, objects.ErrInvalid
		}
	}
	row, err := h.Owned.Get(ctx, db, id)
	if err != nil {
		return nil, err
	}
	ev := Events{DB: h.Store}
	var subjects []string
	out := Item{}
	if db == "workstreams" {
		subjects = []string{"workstreams/" + textField(row, "id")}
		if tasks, _, err := h.Tasks.scopeTasks(ctx, []Item{row}); err == nil {
			for _, t := range tasks {
				subjects = append(subjects, t.ID)
			}
		}
	} else {
		out["milestones"] = h.milestones(ctx, row)
		set := LoadSettings(ctx, h.Store)
		m := DeriveMomentum(set.Momentum, h.themeSignals(ctx, textField(row, "id"), time.Now(), set))
		out["momentum"] = Item{"label": m.Label, "score": m.Score, "text": m.Text, "signals": m.Signals, "series": h.themeSeries(ctx, row, 7)}
	}
	events, next, err := ev.Timeline(ctx, textField(row, "id"), subjects, facet, before, limit)
	if err != nil {
		return nil, err
	}
	out["events"], out["next_before_seq"] = events, next
	return out, nil
}

// milestones are approved (the theme's approval time), first_done and half_done (from Torque task
// counts and the moments those tasks moved). The first unreached one is "next", later ones pending.
func (h *HTTP) milestones(ctx context.Context, theme Item) []Item {
	done, total := 0, 0
	if ws, err := h.Owned.themeWorkstreams(ctx, theme); err == nil && len(ws) > 0 {
		if tasks, _, err := h.Tasks.scopeTasks(ctx, ws); err == nil {
			for _, t := range tasks {
				if _, ok := Lane(t.Status); !ok {
					continue
				}
				total++
				if t.Status == "done" {
					done++
				}
			}
		}
	}
	var firstDone, halfDone int64
	rows, err := h.Store.Reader.QueryContext(ctx, `SELECT at_ms, delta_json FROM planner_events WHERE theme_id=? AND kind='task' ORDER BY seq`, textField(theme, "id"))
	if err == nil {
		defer rows.Close()
		n := 0
		for rows.Next() {
			var at int64
			var raw string
			if rows.Scan(&at, &raw) != nil {
				continue
			}
			d := object(mustDecode(raw))
			n += int(number(d["done"]))
			if n >= 1 && firstDone == 0 {
				firstDone = at
			}
			if total > 0 && n*2 >= total && halfDone == 0 {
				halfDone = at
			}
		}
	}
	state := func(reached bool, at string, seenNext *bool) Item {
		m := Item{}
		switch {
		case reached:
			m["state"] = "reached"
			if at != "" {
				m["at"] = at
			}
		case !*seenNext:
			m["state"], *seenNext = "next", true
		default:
			m["state"] = "pending"
		}
		return m
	}
	stamp := func(ms int64) string {
		if ms == 0 {
			return ""
		}
		return time.UnixMilli(ms).UTC().Format(time.RFC3339)
	}
	next := false
	approved := state(textField(theme, "approved_at") != "", textField(theme, "approved_at"), &next)
	first := state(done >= 1, stamp(firstDone), &next)
	half := state(total > 0 && done*2 >= total, stamp(halfDone), &next)
	approved["key"], first["key"], half["key"] = "approved", "first_done", "half_done"
	out := []Item{approved, first, half}
	if rel := h.releaseMilestone(ctx, theme, &next); rel != nil {
		out = append(out, rel)
	}
	return out
}

// releaseMilestone is the optional release signal of a theme: its "release" field, an object {label, note?, task_id?,
// released_at?} (or just a label string). It is reached when released_at is set or the named Torque task is done;
// a theme without the field has no release milestone.
func (h *HTTP) releaseMilestone(ctx context.Context, theme Item, seenNext *bool) Item {
	var label, note, taskID, at string
	switch v := theme["release"].(type) {
	case string:
		label = v
	case map[string]any:
		label, note, taskID, at = textField(v, "label"), textField(v, "note"), textField(v, "task_id"), textField(v, "released_at")
	}
	if label == "" {
		return nil
	}
	reached := at != ""
	if !reached && taskID != "" && h.Tasks != nil {
		if t, err := h.Tasks.Get(ctx, taskID); err == nil && t.Status == "done" {
			reached, at = true, t.UpdatedAt
		}
	}
	m := Item{"key": "release", "label": label}
	switch {
	case reached:
		m["state"] = "reached"
		if at != "" {
			m["at"] = at
		}
	case !*seenNext:
		m["state"], *seenNext = "next", true
	default:
		m["state"] = "pending"
	}
	if note != "" {
		m["note"] = note
	}
	return m
}

func mustDecode(raw string) Item {
	m, _ := decodeMap([]byte(raw))
	if m == nil {
		return Item{}
	}
	return m
}

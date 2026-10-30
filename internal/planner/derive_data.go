package planner

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/hollis-labs/tachyon/internal/plannerapi"
)

// themeSignals counts the events momentum is derived from for one theme.
func (h *HTTP) themeSignals(ctx context.Context, themeID string, now time.Time, set PlannerSettings) Signals {
	c := set.Momentum
	win := time.Duration(c.WindowHours) * time.Hour
	s := Signals{HoursSinceLast: -1, StaleHours: float64(set.StaleDays) * 24}
	rows, err := h.Store.Reader.QueryContext(ctx, `SELECT at_ms, kind, delta_json FROM planner_events WHERE theme_id=? AND at_ms>=?`, themeID, now.Add(-2*win).UnixMilli())
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var at int64
			var kind, delta string
			if rows.Scan(&at, &kind, &delta) != nil {
				continue
			}
			recent := at >= now.Add(-win).UnixMilli()
			done := int(number(mustDecode(delta)["done"])) > 0
			switch {
			case kind == "task" && done:
				add(&s.RecentDone, &s.PriorDone, recent)
			case kind == "pr":
				add(&s.RecentPRs, &s.PriorPRs, recent)
			case kind != "task" && kind != "blocker":
				add(&s.RecentActivity, &s.PriorActivity, recent)
			}
		}
	}
	var last sql.NullInt64
	_ = h.Store.Reader.QueryRowContext(ctx, `SELECT max(at_ms) FROM planner_events WHERE theme_id=?`, themeID).Scan(&last)
	if last.Valid {
		s.HoursSinceLast = now.Sub(time.UnixMilli(last.Int64)).Hours()
	}
	s.Blocked = h.snapshotCount(ctx, themeID, "blocked")
	return s
}

func add(recent, prior *int, isRecent bool) {
	if isRecent {
		*recent++
	} else {
		*prior++
	}
}

// snapshotCount counts a theme's tasks currently in a Torque status, from the ingest snapshots.
func (h *HTTP) snapshotCount(ctx context.Context, themeID, status string) int {
	var n int
	_ = h.Store.Reader.QueryRowContext(ctx, `SELECT count(*) FROM planner_ingest_state WHERE k LIKE ? AND v=?`, "task:"+themeID+":%", status).Scan(&n)
	return n
}

// needs is the derivation for one item with its "since".
type needs struct {
	Reasons []string
	Since   string
}

// linkedOpenDecisions lists the proposed decisions tied to a theme by an id array, priority_ids or a relation.
func linkedOpenDecisions(theme Item, decisions []Item) []Item {
	out := []Item{}
	tid, legacy := textField(theme, "id"), textField(theme, "legacy_id")
	for _, d := range decisions {
		if textField(d, "status") != "proposed" {
			continue
		}
		did, dlegacy := textField(d, "id"), textField(d, "legacy_id")
		hit := false
		for _, v := range array(theme["decision_ids"]) {
			hit = hit || fmt.Sprint(v) == did || (dlegacy != "" && fmt.Sprint(v) == dlegacy)
		}
		for _, v := range array(d["priority_ids"]) {
			hit = hit || fmt.Sprint(v) == tid || (legacy != "" && fmt.Sprint(v) == legacy)
		}
		for _, r := range relationsOf(d) {
			hit = hit || r.To.ID == tid
		}
		for _, r := range relationsOf(theme) {
			hit = hit || r.To.ID == did
		}
		if hit {
			out = append(out, d)
		}
	}
	return out
}

func (h *HTTP) lastEvent(ctx context.Context, themeID string) (time.Time, bool) {
	var last sql.NullInt64
	_ = h.Store.Reader.QueryRowContext(ctx, `SELECT max(at_ms) FROM planner_events WHERE theme_id=?`, themeID).Scan(&last)
	if !last.Valid {
		return time.Time{}, false
	}
	return time.UnixMilli(last.Int64), true
}

func earliest(times ...string) string {
	out := ""
	for _, t := range times {
		if t != "" && (out == "" || t < out) {
			out = t
		}
	}
	return out
}

func (h *HTTP) themeNeeds(ctx context.Context, theme Item, decisions []Item, now time.Time, set PlannerSettings) needs {
	id := textField(theme, "id")
	stale := time.Duration(set.StaleDays) * 24 * time.Hour
	open := linkedOpenDecisions(theme, decisions)
	f := NeedFacts{DB: "priorities", Status: textField(theme, "status"), ReviewTasks: h.snapshotCount(ctx, id, "review"), BlockedTasks: h.snapshotCount(ctx, id, "blocked"), OpenDecision: len(open) > 0, StaleAfter: stale, Idle: 1 << 62}
	last, ok := h.lastEvent(ctx, id)
	if ok {
		f.Idle = now.Sub(last)
	}
	n := needs{Reasons: DeriveNeedYou(f)}
	times := []string{}
	for _, r := range n.Reasons {
		switch r {
		case "review":
			times = append(times, textField(theme, "updated"))
		case "decide":
			for _, d := range open {
				times = append(times, textField(d, "created"))
			}
		case "stale":
			if ok {
				times = append(times, last.Add(stale).UTC().Format(time.RFC3339))
			}
		}
	}
	n.Since = earliest(times...)
	return n
}

// enrich adds need_you (and momentum for themes) to listed or fetched items, then applies the
// need_you filter (review|decide|blocked|stale|any).
func (h *HTTP) enrich(ctx context.Context, db string, items []Item, filter string) ([]Item, error) {
	if db != "priorities" && db != "decisions" {
		if filter != "" {
			return []Item{}, nil
		}
		return items, nil
	}
	set := LoadSettings(ctx, h.Store)
	now := time.Now()
	decisions, err := h.Owned.List(ctx, "decisions", nil, "")
	if err != nil {
		return nil, err
	}
	out := []Item{}
	for _, it := range items {
		var n needs
		if db == "priorities" {
			n = h.themeNeeds(ctx, it, decisions, now, set)
			m := DeriveMomentum(set.Momentum, h.themeSignals(ctx, textField(it, "id"), now, set))
			it["momentum"] = Item{"label": m.Label, "score": m.Score, "text": m.Text}
		} else {
			n.Reasons = DeriveNeedYou(NeedFacts{DB: "decisions", Status: textField(it, "status")})
			if len(n.Reasons) > 0 {
				n.Since = textField(it, "created")
			}
		}
		var since any
		if n.Since != "" {
			since = n.Since
		}
		it["need_you"] = Item{"reasons": n.Reasons, "since": since}
		if filter != "" && !(len(n.Reasons) > 0 && (filter == "any" || slices.Contains(n.Reasons, filter))) {
			continue
		}
		out = append(out, it)
	}
	return out, nil
}

// NeedYou answers GET /need-you: every theme and decision that needs the operator.
func (h *HTTP) NeedYou(ctx context.Context) (Item, error) {
	set := LoadSettings(ctx, h.Store)
	byReason := Item{}
	for _, r := range plannerapi.NeedYouReasons {
		byReason[r] = 0
	}
	byLens := Item{"themes": 0, "decisions": 0}
	items := []Item{}
	total := 0
	for _, db := range []string{"priorities", "decisions"} {
		list, err := h.Owned.List(ctx, db, nil, "")
		if err != nil {
			return nil, err
		}
		list, err = h.enrich(ctx, db, list, "any")
		if err != nil {
			return nil, err
		}
		for _, it := range list {
			reasons := object(it["need_you"])["reasons"].([]string)
			total++
			byLens[PublicDB(db)] = int(number(byLens[PublicDB(db)])) + 1
			for _, r := range reasons {
				byReason[r] = int(number(byReason[r])) + 1
			}
			items = append(items, Item{"db": PublicDB(db), "id": it["id"], "reasons": reasons})
		}
	}
	return Item{"total": total, "by_reason": byReason, "by_lens": byLens, "items": items, "stale_days": set.StaleDays}, nil
}

// Momentum answers GET /momentum: the portfolio label (mean of active themes' scores) and ingest health.
func (h *HTTP) Momentum(ctx context.Context) (Item, error) {
	set := LoadSettings(ctx, h.Store)
	themes, err := h.Owned.List(ctx, "priorities", nil, "")
	if err != nil {
		return nil, err
	}
	now := time.Now()
	by := Item{"rising": 0, "steady": 0, "cooling": 0, "stalled": 0}
	var sum float64
	n, done, prs := 0, 0, 0
	for _, th := range themes {
		switch textField(th, "status") {
		case "approved", "ready":
		default:
			continue
		}
		sig := h.themeSignals(ctx, textField(th, "id"), now, set)
		m := DeriveMomentum(set.Momentum, sig)
		by[m.Label] = int(number(by[m.Label])) + 1
		sum, n = sum+m.Score, n+1
		done, prs = done+sig.RecentDone, prs+sig.RecentPRs
	}
	out := Item{"by_label": by, "ingest": h.ingestHealth(ctx)}
	if n == 0 {
		out["label"], out["score"], out["text"] = "stalled", 0.0, "No active themes."
		return out, nil
	}
	mean := sum / float64(n)
	label := "steady"
	switch {
	case mean >= 0.6:
		label = "rising"
	case mean < 0.25:
		label = "cooling"
	}
	if int(number(by["stalled"])) == n {
		label = "stalled"
	}
	out["label"], out["score"] = label, clamp01(mean)
	out["text"] = fmt.Sprintf("%s across %s: %s and %s in %dh.", strings.ToUpper(label[:1])+label[1:], plural(n, "theme", "themes"), plural(done, "task", "tasks")+" done", plural(prs, "PR", "PRs")+" merged", set.Momentum.WindowHours)
	return out, nil
}

// ingestHealth reports the worker's last pass: degraded when it never ran, last ran over 5 minutes
// ago, or a step failed.
func (h *HTTP) ingestHealth(ctx context.Context) Item {
	get := func(k string) string { v, _ := stateGet(ctx, h.Store.Reader, k); return v }
	failed := get("failed_steps")
	ok := func(step string) string {
		if strings.Contains(failed, step) {
			return "error"
		}
		return "ok"
	}
	last, _ := strconv.ParseInt(get("last_pass_ms"), 10, 64)
	degraded := last == 0 || failed != "" || time.Since(time.UnixMilli(last)) > 5*time.Minute
	var lastEv sql.NullInt64
	_ = h.Store.Reader.QueryRowContext(ctx, `SELECT max(at_ms) FROM planner_events`).Scan(&lastEv)
	var lastAt any
	if lastEv.Valid {
		lastAt = time.UnixMilli(lastEv.Int64).UTC().Format(time.RFC3339)
	}
	return Item{"degraded": degraded, "last_event_at": lastAt, "sources": Item{"revisions": ok("revisions"), "torque": ok("torque"), "forge": ok("prs")}}
}

// themeSeries is the progress series for the timeline: done count per day, newest last, derived from
// today's done count and the done deltas recorded since.
func (h *HTTP) themeSeries(ctx context.Context, theme Item, days int) []Item {
	id := textField(theme, "id")
	done := h.snapshotCount(ctx, id, "done")
	total := 0
	for _, st := range []string{"todo", "backlog", "queued", "paused", "doing", "review", "blocked", "done"} {
		total += h.snapshotCount(ctx, id, st)
	}
	if total == 0 {
		return []Item{}
	}
	now := time.Now().UTC()
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	out := make([]Item, 0, days)
	for i := days - 1; i >= 0; i-- {
		at := day.AddDate(0, 0, -i)
		end := at.AddDate(0, 0, 1)
		if i == 0 {
			end = now
		}
		var later sql.NullInt64
		_ = h.Store.Reader.QueryRowContext(ctx, `SELECT coalesce(sum(json_extract(delta_json,'$.done')),0) FROM planner_events WHERE theme_id=? AND kind='task' AND at_ms>=?`, id, end.UnixMilli()).Scan(&later)
		d := done - int(later.Int64)
		if d < 0 {
			d = 0
		}
		out = append(out, Item{"at": at.Format(time.RFC3339), "done": d, "total": total})
	}
	return out
}

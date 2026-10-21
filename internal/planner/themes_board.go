package planner

import (
	"context"
	"sort"
	"sync"
	"time"
)

// The theme board's sections are display words derived from stored status and
// task activity; none of them is stored. In flight is an approved theme with
// work under way, cleared is approved and waiting its turn in the sequence.
const (
	themeInFlight = "in_flight"
	themeCleared  = "cleared"
	themeQueued   = "queued"
	themeParked   = "parked"
	themeLanded   = "landed"
	themeProposed = "proposed"
)

const defaultThemeWindowHours = 14 * 24

// themeStatus folds the earlier priority statuses into the current lifecycle.
// A legacy "active" is not treated as approved: approval is never inferred.
func themeStatus(status string) string {
	switch status {
	case "draft", "active", "exploring", "queued":
		return "proposed"
	case "achieved":
		return "done"
	}
	return status
}

func matchesRef(row Item, ref string) bool {
	return ref != "" && (textField(row, "id") == ref || textField(row, "legacy_id") == ref)
}

func (s *Owned) themeBoard(ctx context.Context, tasks *Tasks, o BoardOptions, includeProposed bool) (Item, error) {
	now := time.Now().UTC()
	themes, err := s.List(ctx, "themes", nil, "")
	if err != nil {
		return nil, err
	}
	workstreams, err := s.List(ctx, "workstreams", nil, "")
	if err != nil {
		return nil, err
	}
	landedSince := now.Add(-time.Duration(o.RecentHours * float64(time.Hour)))

	// Select the themes on the board before reading tasks, so Torque is asked
	// only about themes someone can see.
	selected := []Item{}
	for _, t := range themes {
		st := themeStatus(textField(t, "status"))
		switch st {
		case "approved", "ready", "paused":
			selected = append(selected, t)
		case "proposed":
			if includeProposed {
				selected = append(selected, t)
			}
		case "done":
			if !boardTime(textField(t, "updated")).Before(landedSince) {
				selected = append(selected, t)
			}
		}
	}

	linked := func(t Item) []Item {
		out := []Item{}
		for _, v := range array(t["workstream_ids"]) {
			ref, _ := v.(string)
			for _, ws := range workstreams {
				if matchesRef(ws, ref) {
					out = append(out, ws)
					break
				}
			}
		}
		return out
	}

	enrichCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	src := newProgressSource(tasks, now)
	progress := make([]Progress, len(selected))
	failed := make([]bool, len(selected))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 6)
	for i, t := range selected {
		wg.Add(1)
		go func(i int, t Item) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-enrichCtx.Done():
				failed[i] = true
				return
			}
			defer func() { <-sem }()
			p, err := src.theme(enrichCtx, t, linked(t))
			if err != nil {
				failed[i] = true
				return
			}
			progress[i] = p
		}(i, t)
	}
	wg.Wait()

	sections := map[string][]Item{themeInFlight: {}, themeCleared: {}, themeQueued: {}, themeParked: {}, themeLanded: {}, themeProposed: {}}
	notices := []string{}
	unavailable := 0
	header := map[string]string{}
	themeByRef := func(ref string) (Item, bool) {
		for _, t := range themes {
			if matchesRef(t, ref) {
				return t, true
			}
		}
		return nil, false
	}
	for i, t := range selected {
		st := themeStatus(textField(t, "status"))
		ws := linked(t)
		row := Item{"source": "theme", "db": "themes", "id": t["id"], "title": t["title"], "status": st, "stored_status": t["status"], "updated": t["updated"]}
		for _, k := range []string{"legacy_id", "priority", "order", "tldr", "tags", "state", "accountable", "executing_owner", "approved_by", "approved_at", "last_verified", "depends_on", "decision_ids", "risk_ids", "workstream_ids", "roadmap_ids", "comments"} {
			if v, ok := t[k]; ok {
				row[k] = v
			}
		}
		if st != "approved" {
			delete(row, "order")
		}
		// Blockers and the next step roll up from the linked workstreams.
		blockers, next := []any{}, ""
		for _, w := range ws {
			blockers = append(blockers, array(w["blockers"])...)
			if next == "" {
				next = textField(w, "next_step")
			}
		}
		row["blocked_count"] = len(blockers)
		if len(blockers) > 0 {
			row["first_blocker"] = blockers[0]
		}
		if next != "" {
			row["next_step"] = next
		}
		unmet := 0
		for _, v := range array(t["depends_on"]) {
			ref, _ := v.(string)
			if dep, ok := themeByRef(ref); ok {
				if d := themeStatus(textField(dep, "status")); d != "done" && d != "archived" {
					unmet++
				}
			} else if ref != "" {
				unmet++
			}
		}
		row["unmet_dependencies"] = unmet
		if failed[i] {
			unavailable++
			row["progress"] = Item{"available": false}
		} else {
			p := progress[i]
			row["progress"] = Item{"available": true, "done": p.Done, "total": p.Total, "open": p.Open, "counts": p.Counts, "linked": p.Linked, "note": p.Note, "as_of": p.AsOf}
			for id, status := range p.statuses {
				if !excludedStatus(status) {
					header[id] = status
				}
			}
			if st == "paused" {
				row["idle_tasks"] = p.Open
			}
		}
		section := ""
		switch st {
		case "approved":
			section = themeCleared
			if !failed[i] && (progress[i].Counts["doing"] > 0 || progress[i].Counts["review"] > 0) {
				section = themeInFlight
			}
		case "ready":
			section = themeQueued
		case "paused":
			section = themeParked
		case "done":
			section = themeLanded
		case "proposed":
			section = themeProposed
		}
		row["section"] = section
		sections[section] = append(sections[section], row)
	}
	if unavailable > 0 {
		notices = append(notices, "Task progress is unavailable for some themes; Torque's task API did not answer.")
	}

	count, totals := map[string]int{}, map[string]int{}
	for k, rows := range sections {
		sort.SliceStable(rows, func(i, j int) bool { return themeLess(k, rows[i], rows[j]) })
		totals[k] = len(rows)
		if len(rows) > o.Limit {
			rows = rows[:o.Limit]
		}
		sections[k] = rows
		count[k] = len(rows)
	}
	done := 0
	for _, st := range header {
		if st == doneStatus {
			done++
		}
	}
	return Item{"scope": "themes", "generated_at": now.Format(time.RFC3339Nano), "recent_hours": o.RecentHours, "limit": o.Limit, "sections": sections, "counts": count, "totals": totals, "notices": notices,
		"progress": Item{"done": done, "total": len(header), "note": "distinct tasks across the themes shown; a task shared by two themes counts once here"}}, nil
}

// themeLess orders rows within one theme section. Approved themes follow the
// manual sequence; ready and proposed themes rank by priority (1 first, 0 =
// unset last); parked and landed are recency lists.
func themeLess(section string, a, b Item) bool {
	switch section {
	case themeInFlight, themeCleared:
		return boardLess(a, b, true)
	case themeQueued, themeProposed:
		ap, apok := rankNumber(a["priority"], false)
		bp, bpok := rankNumber(b["priority"], false)
		if apok != bpok {
			return apok
		}
		if apok && ap != bp {
			return ap < bp
		}
	}
	return boardLess(a, b, false)
}

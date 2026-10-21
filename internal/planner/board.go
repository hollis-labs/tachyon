package planner

import (
	"context"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/hollis-labs/tachyon/internal/objects"
)

type BoardOptions struct {
	Scope                    string
	RecentHours, ActiveHours float64
	Limit                    int
	// IncludeProposed adds proposed themes to the theme board.
	IncludeProposed bool
}

func boardTime(value string) time.Time {
	for _, format := range []string{time.RFC3339Nano, "2006-01-02"} {
		if t, err := time.Parse(format, value); err == nil {
			return t
		}
	}
	return time.Time{}
}
func contains(values any, want string) bool {
	for _, v := range array(values) {
		if v == want {
			return true
		}
	}
	return false
}

// boardLess orders two rows of one section. The activity feed and the landed
// section are recency lists (updated desc, then id). Curated sections are
// ranked: a manual order beats everything, then priority 1..5 (0 means unset
// and sorts after), then recency. Editing a row therefore never changes its
// rank, only a deliberate reorder or priority change does.
func boardLess(a, b Item, ranked bool) bool {
	if ranked {
		ao, aok := rankNumber(a["order"], true)
		bo, bok := rankNumber(b["order"], true)
		if aok != bok {
			return aok
		}
		if aok && ao != bo {
			return ao < bo
		}
		ap, apok := rankNumber(a["priority"], false)
		bp, bpok := rankNumber(b["priority"], false)
		if apok != bpok {
			return apok
		}
		if apok && ap != bp {
			return ap < bp
		}
	}
	au, bu := textField(a, "updated"), textField(b, "updated")
	if au == bu {
		return textField(a, "id") < textField(b, "id")
	}
	return au > bu
}

// rankNumber reports whether a row carries a usable ranking value. Priority 0
// is "unset"; an explicit order of 0 is a real position.
func rankNumber(v any, zeroIsSet bool) (float64, bool) {
	if v == nil {
		return 0, false
	}
	n := number(v)
	if n == 0 && !zeroIsSet {
		return 0, false
	}
	return n, true
}

func (s *Owned) Board(ctx context.Context, tasks *Tasks, o BoardOptions) (Item, error) {
	if o.Scope == "" {
		o.Scope = "curated"
	}
	if o.Scope != "curated" && o.Scope != "all" && o.Scope != "themes" {
		return nil, objects.ErrInvalid
	}
	if o.RecentHours == 0 {
		o.RecentHours = 72
		if o.Scope == "themes" {
			o.RecentHours = defaultThemeWindowHours
		}
	}
	if o.ActiveHours == 0 {
		o.ActiveHours = 168
	}
	if o.Limit == 0 {
		o.Limit = 50
	}
	if o.RecentHours < 0 || o.RecentHours > 8760 || o.ActiveHours < 0 || o.ActiveHours > 8760 || o.Limit < 1 || o.Limit > 500 {
		return nil, objects.ErrInvalid
	}
	if o.Scope == "themes" {
		return s.themeBoard(ctx, tasks, o, o.IncludeProposed)
	}
	now := time.Now().UTC()
	recent := now.Add(-time.Duration(o.RecentHours * float64(time.Hour)))
	sections := map[string][]Item{"in_flight": {}, "landed": {}, "pre_flight": {}, "holds": {}}
	notices := []string{}
	rowsByDB := map[string][]Item{}
	for _, db := range []string{"workstreams", "roadmap", "decisions", "risks", "inbox"} {
		rows, err := s.List(ctx, db, nil, "")
		if err != nil {
			return nil, err
		}
		rowsByDB[db] = rows
	}
	boardIDs := map[string]bool{}
	for _, v := range rowsByDB["workstreams"] {
		if contains(v["tags"], "board") {
			boardIDs[textField(v, "id")] = true
			boardIDs[textField(v, "legacy_id")] = true
		}
	}
	for _, db := range []string{"workstreams", "roadmap", "decisions", "risks", "inbox"} {
		for _, v := range rowsByDB[db] {
			if o.Scope == "curated" {
				linked := contains(v["tags"], "board")
				for _, field := range []string{"workstream_ids", "related_ids"} {
					for _, x := range array(v[field]) {
						if id, ok := x.(string); ok && boardIDs[id] {
							linked = true
						}
					}
				}
				if id := textField(v, "workstream_id"); id != "" && boardIDs[id] {
					linked = true
				}
				if !linked {
					continue
				}
				if db == "roadmap" {
					continue
				}
			}
			status := textField(v, "status")
			section := ""
			switch db {
			case "workstreams", "roadmap":
				switch status {
				case "active":
					section = "in_flight"
				case "draft", "planned":
					section = "pre_flight"
				case "paused", "deferred":
					section = "holds"
				case "completed", "closed":
					if !boardTime(textField(v, "updated")).Before(recent) {
						section = "landed"
					}
				}
			case "decisions":
				if status == "proposed" {
					section = "holds"
				}
			case "risks":
				if status == "open" || status == "mitigating" {
					section = "holds"
				}
			case "inbox":
				if status == "new" {
					section = "holds"
				}
			}
			row := Item{"source": strings.TrimSuffix(db, "s"), "db": db, "id": v["id"], "title": v["title"], "status": v["status"], "updated": v["updated"]}
			if db == "inbox" || db == "roadmap" {
				row["source"] = db
			}
			for _, k := range []string{"priority", "order", "phase", "owner", "tldr", "next_step", "blockers", "tags", "workstream_id"} {
				if x, ok := v[k]; ok {
					row[k] = x
				}
			}
			// A row lives in exactly one section. Blockers travel on the row
			// so the UI can badge them; they never add a second copy.
			if section != "" {
				sections[section] = append(sections[section], row)
			}
		}
	}
	// Enrichment failure is visible and cannot prevent the owned board loading.
	enrichCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 6)
	counts := map[string]map[string]int{}
	failed := false
	selected := map[string]bool{}
	for _, rows := range sections {
		for _, row := range rows {
			if row["db"] == "workstreams" {
				selected[textField(row, "id")] = true
			}
		}
	}
	for _, ws := range rowsByDB["workstreams"] {
		if !selected[textField(ws, "id")] {
			continue
		}
		wg.Add(1)
		go func(ws Item) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-enrichCtx.Done():
				mu.Lock()
				failed = true
				mu.Unlock()
				return
			}
			defer func() { <-sem }()
			out, err := tasks.ForWorkstream(enrichCtx, ws)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				failed = true
			} else {
				counts[textField(ws, "id")] = out.Counts
			}
		}(ws)
	}
	wg.Wait()
	for _, rows := range sections {
		for _, row := range rows {
			if c, ok := counts[textField(row, "id")]; ok {
				row["task_counts"] = c
			}
		}
	}
	if failed {
		notices = append(notices, "Some execution task counts are unavailable from Torque's task API.")
	}
	if o.Scope == "all" {
		taskCtx, stop := context.WithTimeout(ctx, 8*time.Second)
		defer stop()
		rows, err := tasks.List(taskCtx, url.Values{"updated_after": {now.Add(-time.Duration(o.ActiveHours * float64(time.Hour))).Format(time.RFC3339)}})
		if err != nil {
			notices = append(notices, "Execution activity is unavailable from Torque's task API.")
		} else {
			for _, v := range rows {
				section := ""
				switch v.Status {
				case "doing", "queued", "review":
					section = "in_flight"
				case "todo", "backlog":
					section = "pre_flight"
				case "blocked", "paused":
					section = "holds"
				case "done":
					if !boardTime(v.UpdatedAt).Before(recent) {
						section = "landed"
					}
				}
				if section != "" {
					sections[section] = append(sections[section], Item{"source": "torque", "id": v.ID, "title": v.Title, "status": v.Status, "priority": v.Priority, "updated": v.UpdatedAt})
				}
			}
		}
	}
	count, totals := map[string]int{}, map[string]int{}
	for k, rows := range sections {
		ranked := o.Scope == "curated" && k != "landed"
		sort.SliceStable(rows, func(i, j int) bool { return boardLess(rows[i], rows[j], ranked) })
		totals[k] = len(rows)
		if len(rows) > o.Limit {
			rows = rows[:o.Limit]
		}
		sections[k] = rows
		count[k] = len(rows)
	}
	return Item{"scope": o.Scope, "generated_at": now.Format(time.RFC3339Nano), "recent_hours": o.RecentHours, "active_hours": o.ActiveHours, "limit": o.Limit, "sections": sections, "counts": count, "totals": totals, "notices": notices}, nil
}

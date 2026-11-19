// Package agentsassign is the Agents assignment join (A5): a session's Torque task -> the Planner theme
// that holds it -> "task k of m" and the done count, plus time-on-task and event count from the collector.
// It reads the Planner and Torque and writes nothing. Results are cached 60 s; any failure yields no
// assignment (null), which the UI hides.
package agentsassign

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/hollis-labs/tachyon/internal/localstore"
	"github.com/hollis-labs/tachyon/internal/planner"
)

const (
	ttl = 60 * time.Second
	// A task that is in no theme costs a scan of every theme and its workstream roll-ups in Torque, so "no
	// assignment" is kept longer than a found one.
	missTTL = 5 * time.Minute
	// buildBudget bounds one background join; past it the join is abandoned and counts as a miss.
	buildBudget = 20 * time.Second
	themesDB    = "themes"
	wsDB        = "workstreams"
	maxDirect   = 100
)

// Assignment is the wire shape (agentsapi Session.assignment).
type Assignment struct {
	ThemeRef string `json:"theme_ref"`
	TaskRef  string `json:"task_ref"`
	Index    int    `json:"index"`
	Count    int    `json:"count"`
	Stats    Stats  `json:"stats"`
}

type Stats struct {
	Done         int   `json:"done"`
	TimeOnTaskMS int64 `json:"time_on_task_ms"`
	Events       int64 `json:"events"`
}

// Planner is what the join needs from the Planner and Torque.
type Planner interface {
	List(ctx context.Context, db string, filters map[string]string, sortBy string) ([]planner.Item, error)
	// Statuses returns Torque statuses for task ids; ids Torque does not know are absent.
	Statuses(ctx context.Context, ids []string) (map[string]string, error)
	// WorkstreamStatuses returns the task id -> status roll-up of a workstream row.
	WorkstreamStatuses(ctx context.Context, ws planner.Item) (map[string]string, error)
}

type Joiner struct {
	Planner Planner
	DB      *localstore.Store
	Now     func() time.Time
	// Async makes Assignment answer from the cache at once and run the join in the background, one at a
	// time. The sessions list calls Assignment for every session on every request, and a join can take many
	// Torque round trips: run inline it held the whole list (the live /agents/sessions route stopped answering).
	// A first call therefore returns nil and a later one returns the result.
	Async bool

	mu       sync.Mutex
	cache    map[string]cached
	building map[string]bool
	slot     chan struct{} // one background join at a time
}

type cached struct {
	at time.Time
	a  *Assignment
}

func (j *Joiner) now() time.Time {
	if j.Now != nil {
		return j.Now()
	}
	return time.Now()
}

// Assignment returns the session's assignment or nil. sessionID scopes the stats; taskRef is the
// session's Tether task id (empty means the session was not launched with a task).
func (j *Joiner) Assignment(ctx context.Context, sessionID, taskRef string) any {
	if taskRef == "" || j == nil || j.Planner == nil {
		return nil
	}
	key := sessionID + "\x00" + taskRef
	now := j.now()
	j.mu.Lock()
	c, ok := j.cache[key]
	if ok && now.Sub(c.at) < keep(c.a) {
		j.mu.Unlock()
		return asAny(c.a)
	}
	if j.Async {
		if !j.building[key] {
			if j.building == nil {
				j.building = map[string]bool{}
			}
			if j.slot == nil {
				j.slot = make(chan struct{}, 1)
			}
			j.building[key] = true
			go j.refresh(key, sessionID, taskRef)
		}
		j.mu.Unlock()
		return asAny(c.a) // the last known answer (nil the first time) while the join runs
	}
	j.mu.Unlock()
	a := j.build(ctx, sessionID, taskRef, now)
	j.store(key, a, now)
	return asAny(a)
}

func keep(a *Assignment) time.Duration {
	if a == nil {
		return missTTL
	}
	return ttl
}

func (j *Joiner) store(key string, a *Assignment, now time.Time) {
	j.mu.Lock()
	if j.cache == nil || len(j.cache) > 2000 {
		j.cache = map[string]cached{}
	}
	j.cache[key] = cached{at: now, a: a}
	delete(j.building, key)
	j.mu.Unlock()
}

// refresh runs one join outside any request, under its own deadline.
func (j *Joiner) refresh(key, sessionID, taskRef string) {
	j.slot <- struct{}{}
	defer func() { <-j.slot }()
	ctx, cancel := context.WithTimeout(context.Background(), buildBudget)
	defer cancel()
	var a *Assignment
	func() {
		defer func() { _ = recover() }() // a failed join is a miss, never a crashed host
		a = j.build(ctx, sessionID, taskRef, j.now())
	}()
	if ctx.Err() != nil {
		a = nil // ran out of time part way: do not publish a partial answer
	}
	j.store(key, a, j.now())
}

// asAny keeps a nil *Assignment from becoming a non-nil interface (JSON null, not {}).
func asAny(a *Assignment) any {
	if a == nil {
		return nil
	}
	return a
}

func ids(v any) []string {
	var out []string
	a, _ := v.([]any)
	for _, x := range a {
		if s, ok := x.(string); ok && s != "" {
			out = append(out, s)
		}
	}
	return out
}

func excluded(st string) bool { return st == "cancelled" || st == "abandoned" || st == "archived" }

func (j *Joiner) build(ctx context.Context, sessionID, taskRef string, now time.Time) *Assignment {
	themes, err := j.Planner.List(ctx, themesDB, nil, "")
	if err != nil {
		return nil
	}
	var workstreams []planner.Item
	wsLoaded := false
	for _, th := range themes {
		if st, _ := th["status"].(string); st == "archived" {
			continue
		}
		statuses := map[string]string{}
		var direct []string
		for _, id := range ids(th["tasks"]) {
			direct = append(direct, id)
		}
		if len(direct) > maxDirect {
			direct = direct[:maxDirect]
		}
		if wsIDs := ids(th["workstream_ids"]); len(wsIDs) > 0 {
			if !wsLoaded {
				workstreams, _ = j.Planner.List(ctx, wsDB, nil, "")
				wsLoaded = true
			}
			want := map[string]bool{}
			for _, id := range wsIDs {
				want[id] = true
			}
			for _, ws := range workstreams {
				if id, _ := ws["id"].(string); want[id] {
					if m, err := j.Planner.WorkstreamStatuses(ctx, ws); err == nil {
						for k, v := range m {
							statuses[k] = v
						}
					}
				}
			}
		}
		member := false
		for _, id := range direct {
			if id == taskRef {
				member = true
			}
		}
		if _, ok := statuses[taskRef]; ok {
			member = true
		}
		if !member {
			continue
		}
		var missing []string
		for _, id := range direct {
			if _, ok := statuses[id]; !ok {
				missing = append(missing, id)
			}
		}
		if len(missing) > 0 {
			if m, err := j.Planner.Statuses(ctx, missing); err == nil {
				for k, v := range m {
					statuses[k] = v
				}
			}
		}
		var all []string
		done := 0
		for id, st := range statuses {
			if excluded(st) {
				continue
			}
			all = append(all, id)
			if st == "done" {
				done++
			}
		}
		sort.Strings(all)
		idx := sort.SearchStrings(all, taskRef)
		if idx >= len(all) || all[idx] != taskRef {
			continue // the task itself is cancelled/archived or unknown to Torque: no "k of m"
		}
		themeID, _ := th["id"].(string)
		a := &Assignment{ThemeRef: "planner:theme/" + themeID, TaskRef: taskRef, Index: idx + 1, Count: len(all), Stats: Stats{Done: done}}
		j.stats(ctx, taskRef, now, &a.Stats)
		return a
	}
	return nil
}

// stats: time on task runs from the first session that worked the task to now (or the last end);
// events counts the collector's derived events across those sessions.
func (j *Joiner) stats(ctx context.Context, taskRef string, now time.Time, s *Stats) {
	if j.DB == nil {
		return
	}
	var first, lastEnd, live int64
	err := j.DB.Reader.QueryRowContext(ctx, `SELECT COALESCE(MIN(NULLIF(started_ns,0)),0), COALESCE(MAX(ended_ns),0), COALESCE(SUM(CASE WHEN ended_ns=0 THEN 1 ELSE 0 END),0) FROM agents_sessions WHERE task_ref=?`, taskRef).Scan(&first, &lastEnd, &live)
	if err != nil || first == 0 {
		return
	}
	end := now.UnixNano()
	if live == 0 && lastEnd > 0 {
		end = lastEnd
	}
	if end > first {
		s.TimeOnTaskMS = (end - first) / int64(time.Millisecond)
	}
	_ = j.DB.Reader.QueryRowContext(ctx, `SELECT COUNT(*) FROM agents_events WHERE session_id IN (SELECT session_id FROM agents_sessions WHERE task_ref=?)`, taskRef).Scan(&s.Events)
}

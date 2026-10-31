package planner

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"
)

// Progress is a theme's done/total, computed from Torque at read time and
// never stored. done counts Torque's terminal-success status only; cancelled,
// abandoned and archived tasks leave the denominator, so abandoning work does
// not read as unfinished and does not read as done.
type Progress struct {
	Done     int            `json:"done"`
	Total    int            `json:"total"`
	Open     int            `json:"open"`
	Counts   map[string]int `json:"counts"`
	Linked   bool           `json:"linked"`
	Note     string         `json:"note,omitempty"`
	Missing  int            `json:"missing,omitempty"` // direct task ids Torque did not return
	AsOf     string         `json:"as_of"`
	statuses map[string]string
}

// MarshalJSON sends null (not 0) for the counts of a theme with no linked tasks: nothing was collected, which
// is not the same as nothing being done.
func (p Progress) MarshalJSON() ([]byte, error) {
	type plain Progress
	if p.Linked {
		return json.Marshal(plain(p))
	}
	return json.Marshal(struct {
		plain
		Done   *int           `json:"done"`
		Total  *int           `json:"total"`
		Open   *int           `json:"open"`
		Counts map[string]int `json:"counts"`
	}{plain: plain(p)})
}

const doneStatus = "done"

func excludedStatus(s string) bool {
	switch s {
	case "cancelled", "abandoned", "archived":
		return true
	}
	return false
}

func newProgress(statuses map[string]string, asOf time.Time) Progress {
	p := Progress{Counts: map[string]int{}, Linked: len(statuses) > 0, AsOf: asOf.UTC().Format(time.RFC3339), statuses: statuses}
	for _, st := range statuses {
		p.Counts[st]++
		if excludedStatus(st) {
			continue
		}
		p.Total++
		if st == doneStatus {
			p.Done++
		} else if openTask(st) {
			p.Open++
		}
	}
	if !p.Linked {
		p.Note = "no tasks linked"
	}
	return p
}

// progressSource memoizes workstream roll-ups for one board build, so a
// workstream shared by several themes is read from Torque once.
type progressSource struct {
	tasks *Tasks
	mu    sync.Mutex
	byWS  map[string]*wsTasks
	asOf  time.Time
}
type wsTasks struct {
	once sync.Once
	out  WorkstreamTasks
	err  error
}

func newProgressSource(tasks *Tasks, asOf time.Time) *progressSource {
	return &progressSource{tasks: tasks, byWS: map[string]*wsTasks{}, asOf: asOf}
}

func (ps *progressSource) workstream(ctx context.Context, ws Item) (WorkstreamTasks, error) {
	id := textField(ws, "id")
	ps.mu.Lock()
	e, ok := ps.byWS[id]
	if !ok {
		e = &wsTasks{}
		ps.byWS[id] = e
	}
	ps.mu.Unlock()
	e.once.Do(func() { e.out, e.err = ps.tasks.ForWorkstream(ctx, ws) })
	return e.out, e.err
}

// theme computes one theme's progress: the set union of distinct task ids under
// its workstreams plus its direct task pointers. A failed read of any part makes
// the whole figure unavailable rather than a misleading partial count.
func (ps *progressSource) theme(ctx context.Context, theme Item, workstreams []Item) (Progress, error) {
	statuses := map[string]string{}
	for _, ws := range workstreams {
		out, err := ps.workstream(ctx, ws)
		if err != nil {
			return Progress{}, err
		}
		for id, st := range out.Statuses {
			statuses[id] = st
		}
	}
	missing := 0
	direct := []string{}
	for _, v := range array(theme["tasks"]) {
		if id, ok := v.(string); ok && taskID.MatchString(id) {
			if _, have := statuses[id]; !have {
				direct = append(direct, id)
			}
		}
	}
	sort.Strings(direct)
	for len(direct) > 0 {
		batch := direct
		if len(batch) > 100 {
			batch = batch[:100]
		}
		direct = direct[len(batch):]
		found, err := ps.tasks.Titles(ctx, batch)
		if err != nil {
			return Progress{}, err
		}
		for _, id := range batch {
			if t := found[id]; t != nil {
				statuses[id] = t.Status
			} else {
				missing++
			}
		}
	}
	p := newProgress(statuses, ps.asOf)
	if missing > 0 {
		p.Missing = missing
		if p.Note == "" {
			p.Note = fmt.Sprintf("%d linked task(s) not found", missing)
		}
	}
	return p, nil
}

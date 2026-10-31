package planner

import (
	"context"
	"database/sql"
	"sort"

	"github.com/hollis-labs/tachyon/internal/objects"
)

// TaskAssignment, when set (by the assignment mount, which imports this package), returns the primary active
// assignment of a Torque task as {id, agent_id|team_id, state}, or nil.
var TaskAssignment func(ctx context.Context, task string) Item

func assignmentOf(ctx context.Context, task string) any {
	if TaskAssignment == nil {
		return nil
	}
	if a := TaskAssignment(ctx, task); a != nil {
		return a
	}
	return nil
}

// timeOnTask sums how long Tether sessions launched with this task ran (end, else last activity, minus start).
// It is null when no session carries the task: nothing was measured.
func (h *HTTP) timeOnTask(ctx context.Context, task string) any {
	if h.Store == nil || h.Store.Reader == nil {
		return nil
	}
	var ms sql.NullInt64
	err := h.Store.Reader.QueryRowContext(ctx, `SELECT SUM((COALESCE(NULLIF(ended_ns,0),last_activity_ns)-started_ns)/1000000) FROM agents_sessions WHERE task_ref=? AND started_ns>0 AND COALESCE(NULLIF(ended_ns,0),last_activity_ns)>=started_ns`, task).Scan(&ms)
	if err != nil || !ms.Valid {
		return nil
	}
	return ms.Int64
}

// TaskContext answers where a Torque task sits in its theme (Agents A5 task chip). The theme is the first
// one whose Torque scope lists the task; a task in no theme answers theme null. Assignment and time on task
// are not derived yet and stay null/zero.
func (h *HTTP) TaskContext(ctx context.Context, task string) (Item, error) {
	if !taskID.MatchString(task) {
		return nil, objects.ErrInvalid
	}
	themes, err := h.Owned.List(ctx, "priorities", nil, "")
	if err != nil {
		return nil, err
	}
	for _, theme := range themes {
		ws, err := h.Owned.themeWorkstreams(ctx, theme)
		if err != nil {
			continue
		}
		tasks, _, err := h.Tasks.scopeTasks(ctx, ws)
		if err != nil {
			continue
		}
		sort.SliceStable(tasks, func(i, j int) bool { return tasks[i].ID < tasks[j].ID })
		done, at := 0, -1
		for i, t := range tasks {
			if t.Status == "done" {
				done++
			}
			if t.ID == task {
				at = i
			}
		}
		if at < 0 {
			continue
		}
		t := tasks[at]
		return Item{
			"task":       Item{"id": t.ID, "title": t.Title, "status": t.Status},
			"theme":      Item{"id": theme["id"], "title": theme["title"], "progress": Item{"done": done, "total": len(tasks)}},
			"index":      at + 1,
			"count":      len(tasks),
			"assignment": assignmentOf(ctx, task),
			"stats":      Item{"done_in_theme": done, "events": 0, "time_on_task_ms": h.timeOnTask(ctx, task)},
		}, nil
	}
	titles, err := h.Tasks.Titles(ctx, []string{task})
	if err != nil {
		return nil, err
	}
	b := titles[task]
	if b == nil {
		return nil, objects.ErrNotFound
	}
	return Item{
		"task":  Item{"id": b.ID, "title": b.Title, "status": b.Status},
		"theme": nil, "index": nil, "count": nil, "assignment": assignmentOf(ctx, task),
		"stats": Item{"done_in_theme": 0, "events": 0, "time_on_task_ms": h.timeOnTask(ctx, task)},
	}, nil
}

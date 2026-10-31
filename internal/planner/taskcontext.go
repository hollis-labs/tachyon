package planner

import (
	"context"
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
			"stats":      Item{"done_in_theme": done, "events": 0, "time_on_task_ms": nil},
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
		"stats": Item{"done_in_theme": 0, "events": 0, "time_on_task_ms": nil},
	}, nil
}

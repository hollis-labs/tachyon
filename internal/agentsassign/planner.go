package agentsassign

import (
	"context"

	"github.com/hollis-labs/tachyon/internal/planner"
)

// Live adapts the real Planner objects and Torque reader to the Planner interface.
type Live struct {
	Owned *planner.Owned
	Tasks *planner.Tasks
}

func (l Live) List(ctx context.Context, db string, f map[string]string, sortBy string) ([]planner.Item, error) {
	return l.Owned.List(ctx, db, f, sortBy)
}

func (l Live) Statuses(ctx context.Context, ids []string) (map[string]string, error) {
	out := map[string]string{}
	found, err := l.Tasks.Titles(ctx, ids)
	if err != nil {
		return out, err
	}
	for id, t := range found {
		if t != nil {
			out[id] = t.Status
		}
	}
	return out, nil
}

func (l Live) WorkstreamStatuses(ctx context.Context, ws planner.Item) (map[string]string, error) {
	r, err := l.Tasks.ForWorkstream(ctx, ws)
	return r.Statuses, err
}

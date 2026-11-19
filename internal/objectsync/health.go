package objectsync

import (
	"context"
	"sort"
	"time"
)

// StallFloor is the shortest gap that counts as a stall; a source is also given three of its own intervals.
const StallFloor = 15 * time.Minute

// SourceHealth is one configured source's sync state as GET .../objects/sync/status reports it.
type SourceHealth struct {
	Name          string    `json:"name"`
	Phase         string    `json:"phase"`
	LastSuccessAt string    `json:"last_success_at"`
	FinishedAt    string    `json:"finished_at"`
	Scheduled     bool      `json:"scheduled"`
	Enabled       bool      `json:"enabled"`
	Interval      string    `json:"interval,omitempty"`
	NextRun       time.Time `json:"next_run,omitempty"`
	Problem       string    `json:"problem,omitempty"`
	// Stale is true when the source has no enabled schedule or has not succeeded within max(3 intervals, StallFloor).
	Stale  bool   `json:"stale"`
	Reason string `json:"reason,omitempty"`
}

// Health reports every configured source, sorted by name. It reads only; nothing is fetched.
func (j *Jobs) Health(ctx context.Context, now time.Time) ([]SourceHealth, error) {
	out := make([]SourceHealth, 0, len(j.sources))
	for id, s := range j.sources {
		state, err := j.coordinator.State(ctx, s.Scope())
		if err != nil {
			return nil, err
		}
		sch, exists, err := j.Store.GetSchedule(ctx, id)
		if err != nil {
			return nil, err
		}
		h := SourceHealth{Name: s.Name(), Phase: state.Phase, LastSuccessAt: state.LastSuccessAt, FinishedAt: state.FinishedAt,
			Scheduled: exists, Enabled: exists && sch.Enabled, Problem: state.Problem}
		if exists {
			h.Interval, h.NextRun = sch.Interval.String(), sch.NextRun
		}
		h.Stale, h.Reason = stale(h, sch.Interval, now)
		out = append(out, h)
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Name < out[b].Name })
	return out, nil
}

func stale(h SourceHealth, interval time.Duration, now time.Time) (bool, string) {
	if !h.Enabled {
		return true, "no enabled schedule: run `tachyon sync-objects --source " + h.Name + " --enable`"
	}
	last, err := time.Parse(stamp, h.LastSuccessAt)
	if h.LastSuccessAt == "" || err != nil {
		return true, "no successful sync recorded"
	}
	limit := 3 * interval
	if limit < StallFloor {
		limit = StallFloor
	}
	if now.Sub(last) > limit {
		return true, "no successful sync since " + last.UTC().Format(time.RFC3339)
	}
	return false, ""
}

package objectsync

import (
	"path/filepath"
	"testing"
	"time"
)

// Health is what the status route and the stall watchdog read: unscheduled and overdue sources are stale.
func TestHealthReportsUnscheduledFreshAndStale(t *testing.T) {
	_, c := testStore(t)
	s := newFake()
	jobs, err := OpenJobs(ctx, filepath.Join(t.TempDir(), "jobs.db"), c, []Source{s})
	if err != nil {
		t.Fatal(err)
	}
	defer jobs.Close()

	h, err := jobs.Health(ctx, time.Now())
	if err != nil || len(h) != 1 {
		t.Fatalf("health: %+v %v", h, err)
	}
	if !h[0].Stale || h[0].Enabled || h[0].Name != "test-projects" {
		t.Fatalf("unscheduled source should be stale: %+v", h[0])
	}

	mustRun(t, c, s)
	if err = jobs.Enable(ctx, s, time.Minute); err != nil {
		t.Fatal(err)
	}
	h, _ = jobs.Health(ctx, time.Now())
	if h[0].Stale || !h[0].Enabled || h[0].LastSuccessAt == "" || h[0].Interval != "1m0s" {
		t.Fatalf("fresh enabled source: %+v", h[0])
	}
	// A 1m job gets the 15m floor, not 3m.
	if h, _ = jobs.Health(ctx, time.Now().Add(10*time.Minute)); h[0].Stale {
		t.Fatalf("10m after a success is within the floor: %+v", h[0])
	}
	if h, _ = jobs.Health(ctx, time.Now().Add(StallFloor+time.Minute)); !h[0].Stale || h[0].Reason == "" {
		t.Fatalf("past the floor should be stale: %+v", h[0])
	}
	if err = jobs.Disable(ctx, s); err != nil {
		t.Fatal(err)
	}
	if h, _ = jobs.Health(ctx, time.Now()); !h[0].Stale || h[0].Enabled {
		t.Fatalf("a disabled schedule is stale: %+v", h[0])
	}
}

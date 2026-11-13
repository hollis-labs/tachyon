package plannerassign

import "testing"

func TestCountsOfBucketsTorqueStatuses(t *testing.T) {
	got := CountsOf(map[string]int{"done": 3, "doing": 1, "in_progress": 1, "review": 2, "blocked": 1, "todo": 2, "backlog": 1, "cancelled": 4, "archived": 1})
	want := WorkCounts{Done: 3, Doing: 4, Blocked: 1, Todo: 3}
	if got != want {
		t.Fatalf("got %+v want %+v", got, want)
	}
}

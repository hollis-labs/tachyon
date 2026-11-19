package agentsassign

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/hollis-labs/tachyon/internal/planner"
)

type fake struct {
	themes, wss []planner.Item
	torque      map[string]string
	wsStatus    map[string]string
	calls       int
	fail        bool
}

func (f *fake) List(_ context.Context, db string, _ map[string]string, _ string) ([]planner.Item, error) {
	f.calls++
	if f.fail {
		return nil, errors.New("down")
	}
	if db == "themes" {
		return f.themes, nil
	}
	return f.wss, nil
}
func (f *fake) Statuses(_ context.Context, ids []string) (map[string]string, error) {
	out := map[string]string{}
	for _, id := range ids {
		if st, ok := f.torque[id]; ok {
			out[id] = st
		}
	}
	return out, nil
}
func (f *fake) WorkstreamStatuses(context.Context, planner.Item) (map[string]string, error) {
	return f.wsStatus, nil
}

func TestAssignmentIndexCountAndDone(t *testing.T) {
	f := &fake{
		themes:   []planner.Item{{"id": "other", "tasks": []any{"X-1"}}, {"id": "chat-gui", "tasks": []any{"CW-3"}, "workstream_ids": []any{"ws1"}}},
		wss:      []planner.Item{{"id": "ws1"}},
		wsStatus: map[string]string{"CW-1": "done", "CW-2": "done", "CW-4": "doing", "CW-5": "cancelled"},
		torque:   map[string]string{"CW-3": "doing"},
	}
	j := &Joiner{Planner: f}
	a, _ := j.Assignment(context.Background(), "s1", "CW-3").(*Assignment)
	if a == nil || a.ThemeRef != "planner:theme/chat-gui" || a.Index != 3 || a.Count != 4 || a.Stats.Done != 2 {
		t.Fatalf("%+v", a)
	}
	before := f.calls
	j.Assignment(context.Background(), "s1", "CW-3")
	if f.calls != before {
		t.Fatal("second read within 60 s is cached")
	}
}

func TestAssignmentNullCases(t *testing.T) {
	ctx := context.Background()
	j := &Joiner{Planner: &fake{themes: []planner.Item{{"id": "t", "tasks": []any{"CW-1"}}}, torque: map[string]string{"CW-1": "cancelled"}}}
	for name, got := range map[string]any{
		"no task":            j.Assignment(ctx, "s", ""),
		"not in any theme":   j.Assignment(ctx, "s", "CW-9"),
		"own task cancelled": j.Assignment(ctx, "s", "CW-1"),
		"planner down":       (&Joiner{Planner: &fake{fail: true}}).Assignment(ctx, "s", "CW-1"),
		"nil joiner":         (*Joiner)(nil).Assignment(ctx, "s", "CW-1"),
	} {
		if got != nil {
			t.Errorf("%s: want nil interface, got %#v", name, got)
		}
	}
}

func TestCacheExpires(t *testing.T) {
	f := &fake{themes: []planner.Item{{"id": "t", "tasks": []any{"CW-1"}}}, torque: map[string]string{"CW-1": "doing"}}
	now := time.Unix(1000, 0)
	j := &Joiner{Planner: f, Now: func() time.Time { return now }}
	j.Assignment(context.Background(), "s", "CW-1")
	n := f.calls
	now = now.Add(61 * time.Second)
	j.Assignment(context.Background(), "s", "CW-1")
	if f.calls == n {
		t.Fatal("expired entry is rebuilt")
	}
}

type slowPlanner struct {
	*fake
	gate chan struct{}
}

func (s slowPlanner) List(ctx context.Context, db string, f map[string]string, sortBy string) ([]planner.Item, error) {
	select {
	case <-s.gate:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return s.fake.List(ctx, db, f, sortBy)
}

// The sessions list asks for every session's assignment on every request. A slow Planner or Torque must
// not hold it: an async joiner answers at once and fills in when the join lands.
func TestAsyncJoinNeverBlocksTheCaller(t *testing.T) {
	f := &fake{themes: []planner.Item{{"id": "t", "tasks": []any{"CW-1"}}}, torque: map[string]string{"CW-1": "doing"}}
	gate := make(chan struct{})
	j := &Joiner{Planner: slowPlanner{f, gate}, Async: true}
	done := make(chan any, 1)
	go func() { done <- j.Assignment(context.Background(), "s", "CW-1") }()
	select {
	case a := <-done:
		if a != nil {
			t.Fatalf("nothing is known yet: %v", a)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the caller was held by a slow join")
	}
	j.Assignment(context.Background(), "s", "CW-1") // a second ask while the join runs starts no second join
	close(gate)
	deadline := time.Now().Add(5 * time.Second)
	for {
		if a, _ := j.Assignment(context.Background(), "s", "CW-1").(*Assignment); a != nil {
			if a.ThemeRef != "planner:theme/t" || a.Count != 1 {
				t.Fatalf("joined: %+v", a)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the join never landed")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if f.calls != 1 {
		t.Errorf("one join ran, not one per ask: %d", f.calls)
	}
}

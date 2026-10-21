package planner

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// fakeTorque serves the two task routes the board reads: a tag-filtered list
// and a single task by id.
func fakeTorque(t *testing.T, byTag map[string][]TaskBrief) *Tasks {
	t.Helper()
	all := map[string]TaskBrief{}
	for _, list := range byTag {
		for _, task := range list {
			all[task.ID] = task
		}
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if id, ok := strings.CutPrefix(r.URL.Path, "/api/v1/tasks/"); ok {
			if task, found := all[id]; found {
				_ = json.NewEncoder(w).Encode(task)
				return
			}
			http.NotFound(w, r)
			return
		}
		items := byTag[r.URL.Query().Get("tags")]
		if items == nil {
			items = []TaskBrief{}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"items": items, "meta": map[string]any{"has_more": false, "next_cursor": ""}})
	}))
	t.Cleanup(srv.Close)
	return NewTasks(srv.URL, "")
}

func themeRows(t *testing.T, s *Owned, tasks *Tasks, includeProposed bool) Item {
	t.Helper()
	out, err := s.Board(context.Background(), tasks, BoardOptions{Scope: "themes", IncludeProposed: includeProposed})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func sectionTitles(board Item, section string) []string {
	return boardTitles(board["sections"].(map[string][]Item)[section])
}

func rowNamed(t *testing.T, board Item, section, title string) Item {
	t.Helper()
	for _, r := range board["sections"].(map[string][]Item)[section] {
		if r["title"] == title {
			return r
		}
	}
	t.Fatalf("%q not in %s: %v", title, section, sectionTitles(board, section))
	return nil
}

func TestThemeBoardSectionsOrderAndProgress(t *testing.T) {
	s, _ := ownedFixture(t)
	ctx := context.Background()
	tasks := fakeTorque(t, map[string][]TaskBrief{
		"alpha": {{ID: "CW-1", Status: "done"}, {ID: "CW-2", Status: "doing"}, {ID: "CW-3", Status: "todo"}, {ID: "CW-4", Status: "cancelled"}},
		"beta":  {{ID: "CW-5", Status: "done"}, {ID: "CW-6", Status: "todo"}, {ID: "CW-1", Status: "done"}},
	})
	ws := func(title, tag string, extra Item) string {
		id := uuid.NewString()
		f := Item{"title": title, "status": "active", "torque_tag": tag}
		for k, v := range extra {
			f[k] = v
		}
		if _, err := s.Create(ctx, "workstreams", id, f, "test"); err != nil {
			t.Fatal(err)
		}
		return id
	}
	alpha := ws("alpha", "alpha", Item{"blockers": []any{"waiting on a decision"}, "next_step": "ship the alpha slice"})
	beta := ws("beta", "beta", nil)
	theme := func(title, status string, extra Item) Item {
		f := Item{"title": title, "status": status}
		for k, v := range extra {
			f[k] = v
		}
		row, err := s.Create(ctx, "themes", uuid.NewString(), f, "operator:test")
		if err != nil {
			t.Fatal(err)
		}
		return row
	}
	first := theme("First", "approved", Item{"order": 10, "workstream_ids": []any{alpha}})
	theme("Second", "approved", Item{"order": 20, "workstream_ids": []any{beta}, "tasks": []any{"CW-1"}, "depends_on": []any{first["id"]}})
	theme("Queued low", "ready", Item{"priority": 3})
	theme("Queued high", "ready", Item{"priority": 1})
	theme("Queued unset", "ready", Item{})
	theme("Parked", "paused", Item{"workstream_ids": []any{alpha}})
	theme("Landed", "done", Item{})
	seedLegacyTheme(t, s, uuid.NewString(), "Unmigrated", "active")
	theme("Archived", "archived", Item{})

	board := themeRows(t, s, tasks, false)
	if got := sectionTitles(board, "in_flight"); !equalTitles(got, []string{"First"}) {
		t.Fatal("in flight", got)
	}
	if got := sectionTitles(board, "cleared"); !equalTitles(got, []string{"Second"}) {
		t.Fatal("cleared", got)
	}
	if got := sectionTitles(board, "queued"); !equalTitles(got, []string{"Queued high", "Queued low", "Queued unset"}) {
		t.Fatal("queued", got)
	}
	if got := sectionTitles(board, "parked"); !equalTitles(got, []string{"Parked"}) {
		t.Fatal("parked", got)
	}
	if got := sectionTitles(board, "landed"); !equalTitles(got, []string{"Landed"}) {
		t.Fatal("landed", got)
	}
	// Unmigrated and archived themes stay off the board; no approval is inferred.
	if len(sectionTitles(board, "proposed")) != 0 {
		t.Fatal("proposed themes are hidden by default")
	}
	if got := sectionTitles(themeRows(t, s, tasks, true), "proposed"); !equalTitles(got, []string{"Unmigrated"}) {
		t.Fatal("legacy active should read as proposed, never approved", got)
	}

	// Progress: cancelled leaves the denominator; open work counts as open.
	p := object(rowNamed(t, board, "in_flight", "First")["progress"])
	if p["available"] != true || p["done"] != 1 || p["total"] != 3 || p["open"] != 2 {
		t.Fatal("First progress", p)
	}
	// Union of workstream tasks and direct pointers; the shared CW-1 counts once.
	p = object(rowNamed(t, board, "cleared", "Second")["progress"])
	if p["done"] != 2 || p["total"] != 3 {
		t.Fatal("Second progress", p)
	}
	// Parked themes report their idle work and leave the tasks untouched.
	if rowNamed(t, board, "parked", "Parked")["idle_tasks"] != 2 {
		t.Fatal("idle tasks")
	}
	// A theme with nothing linked says so rather than showing 0/0.
	p = object(rowNamed(t, board, "queued", "Queued high")["progress"])
	if p["linked"] != false || p["note"] != "no tasks linked" || p["total"] != 0 {
		t.Fatal("unlinked progress", p)
	}
	// Roll-ups from workstreams and dependency state.
	first = rowNamed(t, board, "in_flight", "First")
	if first["blocked_count"] != 1 || first["first_blocker"] != "waiting on a decision" || first["next_step"] != "ship the alpha slice" {
		t.Fatal("workstream roll-up", first)
	}
	if rowNamed(t, board, "cleared", "Second")["unmet_dependencies"] != 1 {
		t.Fatal("dependency on a theme that is not done is unmet")
	}
	// Header total counts distinct tasks across themes shown: CW-1,2,3,5,6.
	hp := object(board["progress"])
	if hp["total"] != 5 || hp["done"] != 2 {
		t.Fatal("header progress", hp)
	}
}

func TestThemeBoardSurvivesTaskSourceFailure(t *testing.T) {
	s, _ := ownedFixture(t)
	ctx := context.Background()
	wsID := uuid.NewString()
	if _, err := s.Create(ctx, "workstreams", wsID, Item{"title": "w", "status": "active", "torque_tag": "x"}, "test"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, "themes", uuid.NewString(), Item{"title": "T", "status": "approved", "order": 10, "workstream_ids": []any{wsID}}, "operator:test"); err != nil {
		t.Fatal(err)
	}
	board := themeRows(t, s, nil, false)
	row := rowNamed(t, board, "cleared", "T")
	if object(row["progress"])["available"] != false || len(array(boardNotices(board))) == 0 {
		t.Fatal("an unavailable task source must show as unavailable, not as 0 of 0", row["progress"], board["notices"])
	}
}

func boardNotices(b Item) any {
	out := []any{}
	for _, n := range b["notices"].([]string) {
		out = append(out, n)
	}
	return out
}

func TestThemeRowsListPendingDecisionsByTitle(t *testing.T) {
	s, _ := ownedFixture(t)
	ctx := context.Background()
	open, err := s.Create(ctx, "decisions", uuid.NewString(), Item{"title": "Pick the auth model", "status": "proposed"}, "test")
	if err != nil {
		t.Fatal(err)
	}
	settled, err := s.Create(ctx, "decisions", uuid.NewString(), Item{"title": "Already settled", "status": "accepted"}, "test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, "themes", uuid.NewString(), Item{"title": "T", "status": "ready", "decision_ids": []any{open["id"], settled["id"], "DEC-gone"}}, "operator:test"); err != nil {
		t.Fatal(err)
	}
	row := rowNamed(t, themeRows(t, s, nil, false), "queued", "T")
	got := array(row["pending_decisions"])
	if len(got) != 2 || textField(object(got[0]), "title") != "Pick the auth model" || textField(object(got[1]), "title") != "DEC-gone" {
		t.Fatal("pending decisions", got)
	}
}

func TestApprovedRowsCarryTheirPlaceInTheWholeSequence(t *testing.T) {
	s, _ := ownedFixture(t)
	ctx := context.Background()
	mk := func(title string, order int) {
		if _, err := s.Create(ctx, "themes", uuid.NewString(), Item{"title": title, "status": "approved", "order": order}, "operator:test"); err != nil {
			t.Fatal(err)
		}
	}
	mk("Third", 30)
	mk("First", 10)
	mk("Second", 20)
	out, err := s.Board(ctx, nil, BoardOptions{Scope: "themes", Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]any{}
	for _, r := range out["sections"].(map[string][]Item)["cleared"] {
		got[textField(r, "title")] = r["rank"]
	}
	// The limit cuts the list to two rows but the ranks are those of the full sequence.
	if len(got) != 2 || got["First"] != 1 || got["Second"] != 2 {
		t.Fatal(got)
	}
	if out["totals"].(map[string]int)["cleared"] != 3 {
		t.Fatal("the total still reports all three")
	}
}

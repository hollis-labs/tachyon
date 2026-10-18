package objects

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/hollis-labs/tachyon/internal/localstore"
)

func TestPlannerCRUDQueriesAssignmentsAndHistory(t *testing.T) {
	s, db, path := open(t)
	project := create(t, s, Project{Common: Common{Name: "Fabric"}})
	workflow := create(t, s, Workflow{Common: Common{Name: "Build"}})
	plan := create(t, s, Plan{Common: Common{Name: "Control plane"}, Status: "draft", Priority: 2, ProjectID: project.ID, WorkflowID: workflow.ID, AcceptanceCriteria: []string{"Local writes survive restart"}})
	parent := create(t, s, Task{Common: Common{Name: "Storage"}, Status: "todo", PlanID: plan.ID, ProjectID: project.ID})
	due := time.Date(2026, 11, 1, 8, 0, 0, 0, time.FixedZone("offset", 3600))
	task := create(t, s, Task{Common: Common{Name: "Migration"}, Status: "todo", Priority: 1, PlanID: plan.ID, ProjectID: project.ID, ParentID: parent.ID, DependsOn: []string{parent.ID}, DueAt: &due})
	agent := create(t, s, Agent{Common: Common{Name: "Builder"}})
	team := create(t, s, Team{Common: Common{Name: "Backend"}})
	assignment := create(t, s, Assignment{Common: Common{Name: "Implement migration"}, AgentID: agent.ID, Target: &Reference{ID: task.ID, Kind: TaskKind}, State: "draft"})
	create(t, s, Assignment{Common: Common{Name: "Own plan"}, TeamID: team.ID, Target: &Reference{ID: plan.ID, Kind: PlanKind}, State: "active"})
	for _, f := range []PlannerFilter{{Kind: TaskKind, Status: "todo", Priority: new(1), ProjectID: project.ID, PlanID: plan.ID, ParentID: parent.ID}, {AssignedTo: agent.ID}} {
		rows, err := s.ListPlanner(ctx, f)
		if err != nil || len(rows) != 1 || rows[0].ID != task.ID {
			t.Fatalf("filtered query: %+v %v", rows, err)
		}
	}
	rows, err := s.ListPlanner(ctx, PlannerFilter{AssignedTo: team.ID})
	if err != nil || len(rows) != 1 || rows[0].ID != plan.ID {
		t.Fatal("team assignment query failed", err)
	}
	var projectedDue string
	if err := db.Reader.QueryRow("SELECT due_at FROM planner_items WHERE object_id=?", task.ID).Scan(&projectedDue); err != nil || projectedDue != timestamp(due) {
		t.Fatalf("due date projection: %s %v", projectedDue, err)
	}
	var ids []string
	after := ""
	for {
		page, err := s.ListPlanner(ctx, PlannerFilter{AfterID: after, Limit: 1})
		if err != nil {
			t.Fatal(err)
		}
		if len(page) == 0 {
			break
		}
		if page[0].ID <= after {
			t.Fatal("nonadvancing cursor")
		}
		ids = append(ids, page[0].ID)
		after = page[0].ID
	}
	if len(ids) != 3 {
		t.Fatalf("pagination lost planning records: %v", ids)
	}
	updated, err := s.Patch(ctx, task.ID, task.Revision, Patch{Definition: json.RawMessage(`{"status":"doing","priority":3}`)}, "operator")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Patch(ctx, task.ID, task.Revision, Patch{Definition: json.RawMessage(`{"status":"done"}`)}, "stale"); !errors.Is(err, ErrConflict) {
		t.Fatal("stale edit accepted", err)
	}
	rows, err = s.ListPlanner(ctx, PlannerFilter{Kind: TaskKind, Status: "doing", Priority: new(3)})
	if err != nil || len(rows) != 1 || rows[0].Revision != updated.Revision {
		t.Fatal("query projection did not follow patch", err)
	}
	deleted, err := s.SetDeleted(ctx, task.ID, updated.Revision, true, "operator")
	if err != nil {
		t.Fatal(err)
	}
	rows, err = s.ListPlanner(ctx, PlannerFilter{AssignedTo: agent.ID})
	if err != nil || len(rows) != 0 {
		t.Fatal("deleted target listed", err)
	}
	rows, err = s.ListPlanner(ctx, PlannerFilter{AssignedTo: agent.ID, IncludeDeleted: true})
	if err != nil || len(rows) != 1 || !rows[0].Deleted() {
		t.Fatal("history target missing", err)
	}
	if _, err = s.SetDeleted(ctx, task.ID, deleted.Revision, false, "operator"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetDeleted(ctx, assignment.ID, assignment.Revision, true, "operator"); err != nil {
		t.Fatal(err)
	}
	rows, err = s.ListPlanner(ctx, PlannerFilter{AssignedTo: agent.ID})
	if err != nil || len(rows) != 0 {
		t.Fatal("deleted assignment still assigns work", err)
	}
	history, err := s.History(ctx, task.ID, 0, 100)
	if err != nil || len(history) != 4 || history[1].Record.Revision != updated.Revision || history[3].Operation != "restore" {
		t.Fatal("history lost", err)
	}
	reopened, err := localstore.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	recovered := New(reopened)
	rows, err = recovered.ListPlanner(ctx, PlannerFilter{AssignedTo: team.ID})
	if err != nil || len(rows) != 1 || rows[0].ID != plan.ID {
		t.Fatal("reopen lost indexed relationships", err)
	}
	restored, err := recovered.Get(ctx, task.ID)
	if err != nil || restored.Deleted() || restored.Revision != history[3].Record.Revision {
		t.Fatal("reopen lost task revision", err)
	}
}

func TestPlannerValidationAndConcurrentGraphEdits(t *testing.T) {
	s, _, _ := open(t)
	a := create(t, s, Task{Common: Common{Name: "A"}, Status: "todo"})
	b := create(t, s, Task{Common: Common{Name: "B"}, Status: "todo"})
	project := create(t, s, Project{Common: Common{Name: "P"}})
	for _, d := range []Definition{
		Task{Common: Common{Name: "bad"}, Status: "invented"},
		Task{Common: Common{Name: "bad"}, Status: "todo", Priority: -1},
		Plan{Common: Common{Name: "bad"}, Status: "draft", Priority: 6},
		Task{Common: Common{Name: "bad"}, Status: "todo", PlanID: project.ID},
		Task{Common: Common{Name: "bad"}, Status: "todo", ParentID: "missing"},
		Task{Common: Common{Name: "bad"}, Status: "todo", DependsOn: []string{a.ID, a.ID}},
		Plan{Common: Common{Name: "bad"}, Status: "draft", AcceptanceCriteria: []string{" "}},
	} {
		if _, err := s.Create(ctx, d, nil, "operator"); !errors.Is(err, ErrInvalid) {
			t.Fatalf("invalid definition accepted: %+v %v", d, err)
		}
	}
	for _, field := range []string{"depends_on", "parent_id"} {
		patch := fmt.Sprintf(`{"%s":%q}`, field, a.ID)
		if field == "depends_on" {
			patch = fmt.Sprintf(`{"depends_on":[%q]}`, a.ID)
		}
		if _, err := s.Patch(ctx, a.ID, a.Revision, Patch{Definition: json.RawMessage(patch)}, "operator"); !errors.Is(err, ErrInvalid) {
			t.Fatal("self-reference accepted", err)
		}
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, pair := range [][2]Record{{a, b}, {b, a}} {
		wg.Add(1)
		go func(from, to Record) {
			defer wg.Done()
			_, err := s.Patch(ctx, from.ID, from.Revision, Patch{Definition: json.RawMessage(fmt.Sprintf(`{"depends_on":[%q]}`, to.ID))}, "operator")
			results <- err
		}(pair[0], pair[1])
	}
	wg.Wait()
	close(results)
	ok, invalid := 0, 0
	for err := range results {
		if err == nil {
			ok++
		} else if errors.Is(err, ErrInvalid) {
			invalid++
		} else {
			t.Fatal(err)
		}
	}
	if ok != 1 || invalid != 1 {
		t.Fatalf("concurrent dependency cycle committed: %d %d", ok, invalid)
	}
	// Parent cycles have independent validation, including indirect ancestry.
	x := create(t, s, Task{Common: Common{Name: "X"}, Status: "todo"})
	y := create(t, s, Task{Common: Common{Name: "Y"}, Status: "todo", ParentID: x.ID})
	z := create(t, s, Task{Common: Common{Name: "Z"}, Status: "todo", ParentID: y.ID})
	if _, err := s.Patch(ctx, x.ID, x.Revision, Patch{Definition: json.RawMessage(fmt.Sprintf(`{"parent_id":%q}`, z.ID))}, "operator"); !errors.Is(err, ErrInvalid) {
		t.Fatal("indirect parent cycle accepted", err)
	}
	row, err := s.Get(ctx, x.ID)
	if err != nil || row.Revision != x.Revision {
		t.Fatal("failed graph mutation persisted", err)
	}
}

func TestPlannerProjectionFollowsEffectiveOwnership(t *testing.T) {
	s, _, _ := open(t)
	at := time.Now().UTC()
	obs := Observation{Key: SourceKey{SourceScope: SourceScope{Provider: "test", Instance: "host", Collection: "tasks", Kind: TaskKind}, ExternalID: "remote"}, ObservedAt: at, Definition: body(t, Task{Common: Common{Name: "Remote"}, Status: "todo"})}
	r := observe(t, s, obs)
	r, err := s.Patch(ctx, r.ID, r.Revision, Patch{Definition: json.RawMessage(`{"status":"doing"}`)}, "operator")
	if err != nil {
		t.Fatal(err)
	}
	obs.ObservedAt = at.Add(time.Second)
	obs.Definition = body(t, Task{Common: Common{Name: "Remote changed"}, Status: "blocked"})
	r = observe(t, s, obs)
	rows, err := s.ListPlanner(ctx, PlannerFilter{Status: "doing"})
	if err != nil || len(rows) != 1 || rows[0].Name != "Remote changed" {
		t.Fatal("projection ignored local overlay", err)
	}
	r, err = s.TakeOwnership(ctx, r.ID, r.Revision, "operator")
	if err != nil {
		t.Fatal(err)
	}
	obs.ObservedAt = at.Add(2 * time.Second)
	obs.Deleted = true
	observe(t, s, obs)
	rows, err = s.ListPlanner(ctx, PlannerFilter{Status: "doing"})
	if err != nil || len(rows) != 1 || rows[0].Deleted() {
		t.Fatal("source deletion removed master", err)
	}
}

package provideractions

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	mesh "github.com/hollis-labs/libs/mesh/core"
	"github.com/hollis-labs/libs/mesh/core/teams"
	"github.com/hollis-labs/tachyon/internal/objects"
)

func workingRun(f *fakeBound) {
	f.form = func(_ string, _ json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(fmt.Sprintf(`{"run":{"id":"run-%d","status":"working"}}`, f.calls.Load())), nil
	}
}
func getRun(t *testing.T, s *Service, id string) objects.Record {
	t.Helper()
	r, e := objects.New(s.Store).Get(ctx, id)
	if e != nil {
		t.Fatal(e)
	}
	return r
}
func TestFormationProjectsOneQualifiedRunFromReviewedTeamRevision(t *testing.T) {
	s, team, f := fixture(t)
	workingRun(f)
	formation := formed(t, s, team)
	if formation.ExecutionRunID == "" || formation.ProjectionProblem != "" {
		t.Fatal(formation)
	}
	run := getRun(t, s, formation.ExecutionRunID)
	var definition objects.Run
	_ = json.Unmarshal(run.Definition, &definition)
	if run.Ownership != "source" || run.Source.Key != runKey(formation, "run-1") || definition.Status != "running" || definition.TeamID != team.ID || definition.StartedAt != nil || definition.EndedAt != nil {
		t.Fatal(run, definition)
	}
	_, e := objects.New(s.Store).Patch(ctx, team.ID, team.Revision, objects.Patch{Definition: json.RawMessage(`{"name":"Later team name"}`)}, "test")
	if e != nil {
		t.Fatal(e)
	}
	s.Bind = func() (Bound, error) { t.Fatal("projection called provider"); return nil, nil }
	repaired, e := s.Project(ctx, formation.ID, "operator:test")
	if e != nil || repaired.ExecutionRunID != run.ID {
		t.Fatal(repaired, e)
	}
	unchanged := getRun(t, s, run.ID)
	if unchanged.Revision != run.Revision || unchanged.Name != "Delivery · run-1" {
		t.Fatal("replay used current team definition", unchanged)
	}
	page, e := s.Runs(ctx, RunFilter{TeamID: team.ID})
	if e != nil || len(page.Items) != 1 || page.Items[0].TeamRevision != team.Revision || page.Items[0].Status != "running" || page.Items[0].ReportedStatus != "working" {
		t.Fatal(page, e)
	}
	links, e := objects.New(s.Store).Links(ctx, run.ID, false)
	if e != nil {
		t.Fatal(e)
	}
	found := false
	for _, l := range links {
		if l.ToID == team.ID && l.Relation == "team" {
			found = true
		}
	}
	if !found {
		t.Fatal("native relationship missing", links)
	}
}
func TestProjectionFailurePreservesProviderSuccessAndRepairsWithoutResending(t *testing.T) {
	s, team, f := fixture(t)
	workingRun(f)
	if e := s.Store.Writer.Submit(ctx, "test", func(ctx context.Context, tx *sql.Tx) error {
		_, e := tx.ExecContext(ctx, `CREATE TRIGGER fail_projection BEFORE INSERT ON provider_run_links BEGIN SELECT RAISE(ABORT,'fixture projection failure'); END`)
		return e
	}); e != nil {
		t.Fatal(e)
	}
	formation := formed(t, s, team)
	if formation.ExecutionRunID != "" || formation.ProjectionProblem == "" {
		t.Fatal(formation)
	}
	runs, e := objects.New(s.Store).List(ctx, objects.Filter{Kind: objects.RunKind})
	if e != nil || len(runs) != 0 {
		t.Fatal("partial object persisted", runs, e)
	}
	retained, e := s.Get(ctx, formation.ID)
	if e != nil || retained.State != "succeeded" || retained.ProjectionProblem == "" {
		t.Fatal(retained, e)
	}
	if e = s.Store.Writer.Submit(ctx, "test", func(ctx context.Context, tx *sql.Tx) error {
		_, e := tx.ExecContext(ctx, "DROP TRIGGER fail_projection")
		return e
	}); e != nil {
		t.Fatal(e)
	}
	s.Bind = func() (Bound, error) { t.Fatal("local repair bound provider credential"); return nil, nil }
	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for range 4 {
		wg.Add(1)
		go func() { defer wg.Done(); _, e := s.Project(ctx, formation.ID, "operator:test"); errs <- e }()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	retained, e = s.Get(ctx, formation.ID)
	if e != nil || retained.State != "succeeded" || retained.ExecutionRunID == "" || retained.ProjectionProblem != "" || f.calls.Load() != 1 {
		t.Fatal(retained, e)
	}
	runs, e = objects.New(s.Store).List(ctx, objects.Filter{Kind: objects.RunKind})
	if e != nil || len(runs) != 1 || runs[0].Revision != 1 {
		t.Fatal("repair duplicated run/history", runs, e)
	}
	if _, e = s.Project(ctx, formation.ID, ""); e == nil {
		t.Fatal("repair without operator")
	}
}
func TestUnknownFormationCannotInventRunAndCatalogHasNoRun(t *testing.T) {
	s, team, f := fixture(t)
	r := prepare(t, s, team)
	f.form = func(string, json.RawMessage) (json.RawMessage, error) { return nil, errors.New("lost response") }
	unknown, e := s.Apply(ctx, ApplyRequest{ID: r.ID, Digest: r.Digest}, "operator:test")
	if e != nil || unknown.State != "unknown" || unknown.ExecutionRunID != "" {
		t.Fatal(unknown, e)
	}
	if _, e = s.Project(ctx, r.ID, "operator:test"); e == nil {
		t.Fatal("unknown formation created a run")
	}
	workingRun(f)
	confirmed, e := s.Apply(ctx, ApplyRequest{ID: r.ID, Digest: r.Digest, Retry: true}, "operator:test")
	if e != nil || confirmed.ExecutionRunID == "" {
		t.Fatal(confirmed, e)
	}
	publication := publication(t, s)
	if _, e = s.Project(ctx, publication.ID, "operator:test"); e == nil {
		t.Fatal("catalog projected as execution")
	}
}
func TestRunActivityIncludesPendingDispatchWithoutChangingPlanningState(t *testing.T) {
	s, team, f := fixture(t)
	workingRun(f)
	formation := formed(t, s, team)
	assignment, e := objects.New(s.Store).Create(ctx, objects.Assignment{Common: objects.Common{Name: "Review"}, TeamID: team.ID, State: "draft", ExternalTarget: &objects.ExternalReference{Provider: "github", Instance: "work", Collection: "pulls", ExternalID: "repo#42"}}, nil, "test")
	if e != nil {
		t.Fatal(e)
	}
	action, e := s.PrepareRuntime(ctx, RuntimeRequest{ID: uuid.NewString(), ObjectID: assignment.ID, ExpectedRevision: assignment.Revision, FormationID: formation.ID, Operation: "assign", Address: "@worker", Body: "Review"}, "operator:test")
	if e != nil || action.ExecutionRunID != formation.ExecutionRunID {
		t.Fatal(action, e)
	}
	page, e := s.Runs(ctx, RunFilter{AssignmentID: assignment.ID})
	if e != nil || len(page.Items) != 0 {
		t.Fatal("preview counted as dispatch", page, e)
	}
	f.form = func(string, json.RawMessage) (json.RawMessage, error) {
		return nil, errors.New("lost delivery response")
	}
	unknown, e := s.Apply(ctx, ApplyRequest{ID: action.ID, Digest: action.Digest}, "operator:test")
	if e != nil || unknown.State != "unknown" {
		t.Fatal(unknown, e)
	}
	page, e = s.Runs(ctx, RunFilter{AssignmentID: assignment.ID})
	if e != nil || len(page.Items) != 1 {
		t.Fatal(page, e)
	}
	activity, e := s.Activity(ctx, ActivityFilter{RunID: formation.ExecutionRunID, AssignmentID: assignment.ID})
	if e != nil || len(activity.Items) != 1 || activity.Items[0].State != "unknown" || activity.Items[0].Attempts != 1 {
		t.Fatal(activity, e)
	}
	raw, _ := json.Marshal(activity)
	if strings.Contains(string(raw), "Review\"") || strings.Contains(string(raw), "payload") {
		t.Fatal("summary leaked briefing", string(raw))
	}
	current, e := objects.New(s.Store).Get(ctx, assignment.ID)
	if e != nil || current.Revision != assignment.Revision || string(current.Definition) != string(assignment.Definition) {
		t.Fatal("dispatch edited planning state", e)
	}
}

type executionRoster struct {
	*fakeBound
	version uint64
	status  mesh.TaskState
}

func (r *executionRoster) Roster(context.Context, string) ([]RosterView, error) {
	return []RosterView{{Run: teams.TeamRun{ID: "run-1", Status: r.status}, Version: r.version, Members: []Member{}}}, nil
}
func TestRosterUpdatesRunEvidenceAndRetainsLocalOverridesAndTombstones(t *testing.T) {
	s, team, f := fixture(t)
	workingRun(f)
	formation := formed(t, s, team)
	run := getRun(t, s, formation.ExecutionRunID)
	run, e := objects.New(s.Store).Patch(ctx, run.ID, run.Revision, objects.Patch{Definition: json.RawMessage(`{"name":"Local label","status":"paused"}`), Metadata: json.RawMessage(`{"purpose":"review"}`)}, "operator:test")
	if e != nil {
		t.Fatal(e)
	}
	reader := &executionRoster{fakeBound: f, version: 8, status: mesh.TaskCompleted}
	s.Bind = func() (Bound, error) { return reader, nil }
	snapshot, e := s.Roster(ctx, RosterRequest{FormationID: formation.ID, Refresh: true})
	if e != nil || snapshot.Problem != "" {
		t.Fatal(snapshot, e)
	}
	current := getRun(t, s, run.ID)
	var effective, observed objects.Run
	_ = json.Unmarshal(current.Definition, &effective)
	_ = json.Unmarshal(current.Source.Definition, &observed)
	if effective.Name != "Local label" || effective.Status != "paused" || observed.Status != "completed" || effective.EndedAt != nil || !strings.Contains(string(current.Metadata), "review") {
		t.Fatal(effective, observed, current.Metadata)
	}
	revision := current.Revision
	reader.version = 7
	reader.status = mesh.TaskWorking
	if _, e = s.Roster(ctx, RosterRequest{FormationID: formation.ID, Refresh: true}); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Project(ctx, formation.ID, "operator:test"); e != nil {
		t.Fatal(e)
	}
	current = getRun(t, s, run.ID)
	if current.Revision != revision {
		t.Fatal("old evidence regressed projection", current)
	}
	current, e = objects.New(s.Store).SetDeleted(ctx, current.ID, current.Revision, true, "operator:test")
	if e != nil {
		t.Fatal(e)
	}
	reader.version = 9
	reader.status = mesh.TaskCompleted
	if _, e = s.Roster(ctx, RosterRequest{FormationID: formation.ID, Refresh: true}); e != nil {
		t.Fatal(e)
	}
	current = getRun(t, s, run.ID)
	if !current.Deleted() {
		t.Fatal("observation restored local tombstone")
	}
	page, e := s.Runs(ctx, RunFilter{})
	if e != nil || len(page.Items) != 0 {
		t.Fatal(page, e)
	}
	page, e = s.Runs(ctx, RunFilter{IncludeDeleted: true})
	if e != nil || len(page.Items) != 1 || !page.Items[0].Deleted || page.Items[0].ReportedStatus != "completed" || page.Items[0].Status != "paused" {
		t.Fatal(page, e)
	}
}
func TestRunAndActivityPaginationAndDuplicateRunProtection(t *testing.T) {
	s, team, f := fixture(t)
	workingRun(f)
	first := formed(t, s, team)
	second := formed(t, s, team)
	one, e := s.Runs(ctx, RunFilter{TeamID: team.ID, Limit: 1})
	if e != nil || len(one.Items) != 1 || one.Items[0].FormationID != second.ID || one.NextBeforeID == "" {
		t.Fatal(one, e)
	}
	two, e := s.Runs(ctx, RunFilter{TeamID: team.ID, BeforeID: one.NextBeforeID, Limit: 1})
	if e != nil || len(two.Items) != 1 || two.Items[0].FormationID != first.ID || two.NextBeforeID != "" {
		t.Fatal(two, e)
	}
	for range 2 {
		if _, e = s.PrepareRuntime(ctx, RuntimeRequest{ID: uuid.NewString(), ObjectID: team.ID, ExpectedRevision: team.Revision, FormationID: first.ID, Operation: "dissolve"}, "operator:test"); e != nil {
			t.Fatal(e)
		}
	}
	cursor := ""
	seen := map[string]bool{}
	for {
		page, e := s.Activity(ctx, ActivityFilter{RunID: first.ExecutionRunID, BeforeID: cursor, Limit: 1})
		if e != nil {
			t.Fatal(e)
		}
		for _, r := range page.Items {
			if seen[r.ID] {
				t.Fatal("duplicate page", r)
			}
			seen[r.ID] = true
		}
		if page.NextBeforeID == "" {
			break
		}
		cursor = page.NextBeforeID
	}
	if len(seen) != 3 {
		t.Fatal("activity skipped records", seen)
	}
	if _, e = s.Activity(ctx, ActivityFilter{RunID: second.ExecutionRunID, BeforeID: first.ID}); e == nil {
		t.Fatal("cross-run cursor accepted")
	}
	f.form = func(string, json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(`{"run":{"id":"run-1","status":"working"}}`), nil
	}
	duplicate := formed(t, s, team)
	if duplicate.ExecutionRunID != "" || duplicate.ProjectionProblem == "" {
		t.Fatal("provider run silently reassigned", duplicate)
	}
	page, e := s.Runs(ctx, RunFilter{})
	if e != nil || len(page.Items) != 2 {
		t.Fatal(page, e)
	}
	for _, limit := range []int{-1, 101} {
		if _, e = s.Runs(ctx, RunFilter{Limit: limit}); e == nil {
			t.Fatal("unbounded query")
		}
	}
}

package objects

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/hollis-labs/tachyon/internal/localstore"
)

func TestOwnedPlannerRecords(t *testing.T) {
	s, _, path := open(t)
	project := create(t, s, Project{Common: Common{Name: "Fabric"}})
	ws := create(t, s, Workstream{Common: Common{Name: "Backend"}, Status: "active"})
	pc := PlanningContext{ProjectID: project.ID, WorkstreamID: ws.ID}
	fields := CaptureFields{Common: Common{Name: "Capture", Description: "Keep detail", Tags: []string{"backend"}, Properties: map[string]any{"custom": true}}, Status: "new", Priority: 2, ProjectID: project.ID, WorkstreamID: ws.ID}
	inbox := create(t, s, InboxItem{CaptureFields: fields, Origin: "operator"})
	fields.Status = "exploring"
	idea := create(t, s, Idea{CaptureFields: fields, Hypothesis: "Durable local ownership"})
	plan := create(t, s, Plan{Common: Common{Name: "Delivery"}, Status: "draft"})
	priority := create(t, s, StrategicPriority{Common: Common{Name: "Ownership"}, PlanningContext: pc, Status: "active", Priority: 1, SuccessCriteria: []string{"Survive restart"}})
	roadmap := create(t, s, RoadmapItem{Common: Common{Name: "Data layer"}, PlanningContext: pc, Status: "planned", Priority: 2, StrategicPriorityID: priority.ID, PlanID: plan.ID})
	decision := create(t, s, Decision{Common: Common{Name: "Use SQLite"}, PlanningContext: pc, Status: "accepted", Target: &Reference{ID: plan.ID, Kind: PlanKind}, Outcome: "Queued writes", Rationale: "Prevent concurrent writer contention", Options: []string{"SQLite", "Postgres"}})
	risk := create(t, s, Risk{Common: Common{Name: "Stale writer"}, PlanningContext: pc, Status: "open", Priority: 2, Likelihood: 3, Impact: 4, Mitigation: "CAS", Target: &Reference{ID: plan.ID, Kind: PlanKind}})
	for _, test := range []struct {
		filter PlannerFilter
		ids    []string
	}{
		{PlannerFilter{Kind: InboxItemKind, Status: "new", ProjectID: project.ID, WorkstreamID: ws.ID}, []string{inbox.ID}},
		{PlannerFilter{Kind: IdeaKind, Status: "exploring"}, []string{idea.ID}},
		{PlannerFilter{StrategicPriorityID: priority.ID, PlanID: plan.ID}, []string{roadmap.ID}},
		{PlannerFilter{TargetID: plan.ID}, []string{decision.ID, risk.ID}},
		{PlannerFilter{Status: "accepted"}, []string{decision.ID}},
	} {
		rows, err := s.ListPlanner(ctx, test.filter)
		if err != nil || len(rows) != len(test.ids) {
			t.Fatalf("query %+v: %+v %v", test.filter, rows, err)
		}
		for i, id := range test.ids {
			if rows[i].ID != id {
				t.Fatalf("unexpected result %s != %s", rows[i].ID, id)
			}
		}
	}
	updated, err := s.Patch(ctx, risk.ID, risk.Revision, Patch{Definition: json.RawMessage(`{"status":"mitigating","likelihood":1}`), Metadata: json.RawMessage(`{"owner":"backend"}`)}, "operator")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Patch(ctx, risk.ID, risk.Revision, Patch{Definition: json.RawMessage(`{"status":"resolved"}`)}, "operator"); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	deleted, err := s.SetDeleted(ctx, risk.ID, updated.Revision, true, "operator")
	if err != nil {
		t.Fatal(err)
	}
	rows, err := s.ListPlanner(ctx, PlannerFilter{TargetID: plan.ID})
	if err != nil || len(rows) != 1 || rows[0].ID != decision.ID {
		t.Fatal("tombstone visible", err)
	}
	history, err := s.History(ctx, risk.ID, 0, 100)
	if err != nil || len(history) != 3 || history[0].Operation != "create" || history[2].Operation != "delete" {
		t.Fatal("history", err)
	}
	reopened, err := localstore.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	stored, err := New(reopened).Get(ctx, risk.ID)
	if err != nil || !stored.Deleted() || stored.Revision != deleted.Revision {
		t.Fatal("lost on reopen", err)
	}
	links, err := New(reopened).Links(ctx, stored.ID, false)
	if err != nil || len(links) != 3 {
		t.Fatal("lost tombstone links", err)
	}
}

func TestPlannerRecordValidation(t *testing.T) {
	s, _, _ := open(t)
	p := create(t, s, Project{Common: Common{Name: "P"}})
	at := time.Now().UTC()
	before := at.Add(-time.Hour)
	for _, d := range []Definition{
		InboxItem{CaptureFields: CaptureFields{Common: Common{Name: "bad"}, Status: "invented"}},
		Idea{CaptureFields: CaptureFields{Common: Common{Name: "bad"}, Status: "draft", Priority: 6}},
		InboxItem{CaptureFields: CaptureFields{Common: Common{Name: "bad"}, Status: "new"}, CapturedAt: new(time.Time)},
		Decision{Common: Common{Name: "bad"}, Status: "accepted", Options: []string{" "}},
		Decision{Common: Common{Name: "bad"}, Status: "accepted", Target: &Reference{ID: p.ID, Kind: TaskKind}},
		Risk{Common: Common{Name: "bad"}, Status: "open", Impact: -1},
		Risk{Common: Common{Name: "bad"}, Status: "open", Likelihood: 6},
		Risk{Common: Common{Name: "bad"}, Status: "open", Target: &Reference{Kind: PlanKind}},
		RoadmapItem{Common: Common{Name: "bad"}, Status: "planned", StartsAt: &at, EndsAt: &before},
		RoadmapItem{Common: Common{Name: "bad"}, Status: "planned", StrategicPriorityID: p.ID},
		StrategicPriority{Common: Common{Name: "bad"}, Status: "active", SuccessCriteria: []string{""}},
	} {
		if _, err := s.Create(ctx, d, nil, "test"); !errors.Is(err, ErrInvalid) {
			t.Fatalf("accepted %+v: %v", d, err)
		}
	}
	good := create(t, s, Risk{Common: Common{Name: "Good"}, Status: "open"})
	if _, err := s.Patch(ctx, good.ID, good.Revision, Patch{Definition: json.RawMessage(`{"invented":true}`)}, "test"); !errors.Is(err, ErrInvalid) {
		t.Fatal("unknown field accepted", err)
	}
	for _, kind := range []Kind{InboxItemKind, IdeaKind, DecisionKind, RiskKind, RoadmapItemKind, StrategicPriorityKind} {
		if _, err := s.ListPlanner(ctx, PlannerFilter{Kind: kind, Status: "invented"}); !errors.Is(err, ErrInvalid) {
			t.Fatal(fmt.Sprint(kind), err)
		}
	}
}

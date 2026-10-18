package objects

import (
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/hollis-labs/tachyon/internal/localstore"
)

func TestOrganizationLinksQueriesAndReopen(t *testing.T) {
	s, _, path := open(t)
	project := create(t, s, Project{Common: Common{Name: "Fabric"}})
	stream := create(t, s, Workstream{Common: Common{Name: "Control surface"}, Status: "active", ProjectID: project.ID})
	epic := create(t, s, Epic{Common: Common{Name: "Own the data"}, Status: "doing", Priority: 2, ProjectID: project.ID, WorkstreamID: stream.ID})
	start := time.Date(2026, 10, 18, 0, 0, 0, 0, time.UTC)
	end := start.Add(14 * 24 * time.Hour)
	sprint := create(t, s, Sprint{Common: Common{Name: "October"}, Status: "active", ProjectID: project.ID, WorkstreamID: stream.ID, StartsAt: &start, EndsAt: &end})
	plan := create(t, s, Plan{Common: Common{Name: "Organization"}, Status: "draft", EpicID: epic.ID, SprintID: sprint.ID, WorkstreamID: stream.ID})
	task := create(t, s, Task{Common: Common{Name: "Persist"}, Status: "todo", PlanID: plan.ID, EpicID: epic.ID, SprintID: sprint.ID, WorkstreamID: stream.ID})
	collection := create(t, s, Collection{Common: Common{Name: "Focus"}, Status: "active", ProjectID: project.ID, WorkstreamID: stream.ID, Members: []Reference{{ID: task.ID, Kind: TaskKind}, {ID: project.ID, Kind: ProjectKind}}})
	for _, filter := range []PlannerFilter{{EpicID: epic.ID}, {SprintID: sprint.ID}, {WorkstreamID: stream.ID, Kind: TaskKind}, {CollectionID: collection.ID}, {ProjectID: project.ID, Kind: SprintKind, Status: "active"}} {
		rows, err := s.ListPlanner(ctx, filter)
		want := 1
		if filter.EpicID != "" || filter.SprintID != "" {
			want = 2
		}
		if err != nil || len(rows) != want {
			t.Fatalf("query %+v: %+v %v", filter, rows, err)
		}
	}
	// Plan membership never implicitly propagates to tasks.
	independent := create(t, s, Task{Common: Common{Name: "Independent"}, Status: "todo", PlanID: plan.ID})
	rows, err := s.ListPlanner(ctx, PlannerFilter{EpicID: epic.ID})
	if err != nil || len(rows) != 2 {
		t.Fatal("unexpected inheritance", rows, err)
	}
	for _, r := range rows {
		if r.ID == independent.ID {
			t.Fatal("inherited epic")
		}
	}
	_, err = s.Patch(ctx, task.ID, task.Revision, Patch{Definition: json.RawMessage(`{"epic_id":null,"sprint_id":null,"workstream_id":null}`)}, "operator")
	if err != nil {
		t.Fatal(err)
	}
	rows, err = s.ListPlanner(ctx, PlannerFilter{Kind: TaskKind, EpicID: epic.ID})
	if err != nil || len(rows) != 0 {
		t.Fatal("stale projection", err)
	}
	if _, err = s.SetDeleted(ctx, stream.ID, stream.Revision, true, "operator"); err != nil {
		t.Fatal(err)
	}
	rows, err = s.ListPlanner(ctx, PlannerFilter{Kind: EpicKind, WorkstreamID: stream.ID})
	if err != nil || len(rows) != 1 {
		t.Fatal("deletion cascaded into linked records", err)
	}
	reopened, err := localstore.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	page, err := New(reopened).ListCollectionMembers(ctx, collection.ID, 0, 0, 100)
	if err != nil || len(page.Members) != 2 || page.Members[0].Record.ID != task.ID {
		t.Fatal("membership lost on reopen", err)
	}
}

func TestOrganizationValidationIsAtomic(t *testing.T) {
	s, _, _ := open(t)
	task := create(t, s, Task{Common: Common{Name: "Task"}, Status: "todo"})
	collection := create(t, s, Collection{Common: Common{Name: "Collection"}, Status: "active"})
	start := time.Now().UTC()
	before := start.Add(-time.Hour)
	invalid := []Definition{
		Epic{Common: Common{Name: "Epic"}, Status: "active"},
		Epic{Common: Common{Name: "Epic"}, Status: "todo", Priority: 6},
		Sprint{Common: Common{Name: "Sprint"}, Status: "doing"},
		Sprint{Common: Common{Name: "Sprint"}, Status: "active", StartsAt: &start},
		Sprint{Common: Common{Name: "Sprint"}, Status: "active", StartsAt: &start, EndsAt: &before},
		Sprint{Common: Common{Name: "Sprint"}, Status: "active", StartsAt: &start, EndsAt: &start},
		Workstream{Common: Common{Name: "Stream"}, Status: "active", WorkflowID: task.ID},
		Collection{Common: Common{Name: "Collection"}, Status: "active", Members: []Reference{{ID: task.ID, Kind: EpicKind}}},
		Collection{Common: Common{Name: "Collection"}, Status: "active", Members: []Reference{{ID: "missing", Kind: TaskKind}}},
		Collection{Common: Common{Name: "Collection"}, Status: "active", Members: []Reference{{ID: collection.ID, Kind: CollectionKind}}},
		Task{Common: Common{Name: "Task"}, Status: "todo", EpicID: task.ID},
		Plan{Common: Common{Name: "Plan"}, Status: "todo", SprintID: "missing"},
	}
	for _, d := range invalid {
		if _, err := s.Create(ctx, d, nil, "test"); !errors.Is(err, ErrInvalid) {
			t.Fatalf("accepted %+v: %v", d, err)
		}
	}
	refs := []Reference{{ID: task.ID, Kind: TaskKind}, {ID: task.ID, Kind: TaskKind}}
	if _, err := s.SetCollectionMembers(ctx, collection.ID, collection.Revision, refs, "test"); !errors.Is(err, ErrInvalid) {
		t.Fatal("duplicate membership accepted", err)
	}
	r, err := s.Get(ctx, collection.ID)
	if err != nil || r.Revision != collection.Revision {
		t.Fatal("invalid edit changed revision", err)
	}
	links, err := s.Links(ctx, collection.ID, false)
	if err != nil || len(links) != 0 {
		t.Fatal("invalid edit left links", err)
	}
}

func TestCollectionOrderHistoryTombstonesAndPaging(t *testing.T) {
	s, _, _ := open(t)
	a := create(t, s, Task{Common: Common{Name: "A"}, Status: "todo"})
	b := create(t, s, Agent{Common: Common{Name: "B"}, Enabled: true})
	c := create(t, s, Workstream{Common: Common{Name: "C"}, Status: "closed"})
	refs := []Reference{{ID: a.ID, Kind: TaskKind}, {ID: b.ID, Kind: AgentKind}, {ID: c.ID, Kind: WorkstreamKind}}
	collection := create(t, s, Collection{Common: Common{Name: "Focus"}, Status: "active", Members: refs})
	create(t, s, Collection{Common: Common{Name: "Also focus"}, Status: "active", Members: refs})
	page, err := s.ListCollectionMembers(ctx, collection.ID, 0, 0, 2)
	if err != nil || len(page.Members) != 2 || page.NextOffset == nil || *page.NextOffset != 2 || page.Members[0].Reference != refs[0] || page.Members[1].Reference != refs[1] {
		t.Fatalf("wrong first page: %+v %v", page, err)
	}
	last, err := s.ListCollectionMembers(ctx, collection.ID, page.Revision, *page.NextOffset, 2)
	if err != nil || len(last.Members) != 1 || last.NextOffset != nil || last.Members[0].Position != 2 || last.Members[0].Reference != refs[2] {
		t.Fatal("wrong last page", last, err)
	}
	if _, err = s.ListCollectionMembers(ctx, collection.ID, 0, 1, 2); !errors.Is(err, ErrInvalid) {
		t.Fatal("unguarded continuation accepted", err)
	}
	if _, err = s.SetDeleted(ctx, b.ID, b.Revision, true, "operator"); err != nil {
		t.Fatal(err)
	}
	refs[0], refs[2] = refs[2], refs[0]
	reordered, err := s.SetCollectionMembers(ctx, collection.ID, collection.Revision, refs, "operator")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ListCollectionMembers(ctx, collection.ID, page.Revision, 2, 2); !errors.Is(err, ErrConflict) {
		t.Fatal("stale page accepted", err)
	}
	page, err = s.ListCollectionMembers(ctx, collection.ID, 0, 0, 100)
	if err != nil || page.Members[0].Record.ID != c.ID || !page.Members[1].Record.Deleted() {
		t.Fatal("reorder/tombstone lost", page, err)
	}
	links, err := s.Links(ctx, collection.ID, false)
	if err != nil || len(links) != 3 || links[0].ToID != c.ID || links[2].ToID != a.ID {
		t.Fatal("ordered links differ", links, err)
	}
	same, err := s.SetCollectionMembers(ctx, collection.ID, reordered.Revision, refs, "operator")
	if err != nil || same.Revision != reordered.Revision {
		t.Fatal("no-op created revision", err)
	}
	history, err := s.History(ctx, collection.ID, 0, 100)
	if err != nil || len(history) != 2 {
		t.Fatal("history lost", err)
	}
	var original Collection
	if err = json.Unmarshal(history[0].Record.Definition, &original); err != nil || original.Members[0].ID != a.ID {
		t.Fatal("historical order changed", err)
	}
	empty, err := s.SetCollectionMembers(ctx, collection.ID, reordered.Revision, nil, "operator")
	if err != nil {
		t.Fatal(err)
	}
	page, err = s.ListCollectionMembers(ctx, collection.ID, empty.Revision, 0, 100)
	if err != nil || len(page.Members) != 0 {
		t.Fatal("clear did not clear", err)
	}
}

func TestConcurrentCollectionReorderHasOneWinner(t *testing.T) {
	s, _, _ := open(t)
	a := create(t, s, Task{Common: Common{Name: "A"}, Status: "todo"})
	b := create(t, s, Task{Common: Common{Name: "B"}, Status: "todo"})
	c := create(t, s, Collection{Common: Common{Name: "Focus"}, Status: "active"})
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, id := range []string{a.ID, b.ID} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			_, err := s.SetCollectionMembers(ctx, c.ID, c.Revision, []Reference{{ID: id, Kind: TaskKind}}, "operator")
			errs <- err
		}(id)
	}
	wg.Wait()
	close(errs)
	success, conflict := 0, 0
	for err := range errs {
		if err == nil {
			success++
		} else if errors.Is(err, ErrConflict) {
			conflict++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatalf("lost update: %d success, %d conflict", success, conflict)
	}
}

func TestClearSourcedCollectionRetainsExplicitEmptyMembership(t *testing.T) {
	s, _, _ := open(t)
	task := create(t, s, Task{Common: Common{Name: "Task"}, Status: "todo"})
	observation := Observation{
		Key:        SourceKey{SourceScope: SourceScope{Provider: "test", Instance: "hub", Collection: "collections", Kind: CollectionKind}, ExternalID: "focus"},
		ObservedAt: time.Now().UTC(),
		Definition: body(t, Collection{Common: Common{Name: "Focus"}, Status: "active", Members: []Reference{{ID: task.ID, Kind: TaskKind}}}),
	}
	collection := observe(t, s, observation)
	cleared, err := s.SetCollectionMembers(ctx, collection.ID, collection.Revision, nil, "operator")
	if err != nil {
		t.Fatal(err)
	}
	observation.ObservedAt = observation.ObservedAt.Add(time.Second)
	observe(t, s, observation)
	page, err := s.ListCollectionMembers(ctx, collection.ID, 0, 0, 100)
	if err != nil || len(page.Members) != 0 || page.Revision != cleared.Revision {
		t.Fatal("source membership reappeared", page, err)
	}
}

package objects

import (
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestOwnedImportIdempotencyAuthorshipAndProvenance(t *testing.T) {
	s, _, _ := open(t)
	at := time.Now().UTC()
	obs := Observation{Key: SourceKey{SourceScope: SourceScope{Provider: "torque", Instance: "https://torque.test", Collection: "tasks", Kind: TaskKind}, ExternalID: "CW-1"}, ObservedAt: at, Definition: body(t, Task{Common: Common{Name: "Imported"}, Status: "todo"})}
	var wg sync.WaitGroup
	results := make(chan Record, 2)
	errs := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() { defer wg.Done(); r, err := s.ImportOwned(ctx, obs, nil, "import"); results <- r; errs <- err }()
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var first Record
	for r := range results {
		if r.Ownership != "tachyon" || r.Revision != 1 || first.ID != "" && first.ID != r.ID {
			t.Fatal("duplicate import")
		}
		first = r
	}
	edited, err := s.Patch(ctx, first.ID, first.Revision, Patch{Definition: json.RawMessage(`{"name":"Authored","status":"doing"}`), Metadata: json.RawMessage(`{"priority_reason":"operator"}`)}, "operator")
	if err != nil {
		t.Fatal(err)
	}
	obs.ObservedAt = at.Add(time.Second)
	obs.Definition = body(t, Task{Common: Common{Name: "Remote changed"}, Status: "done"})
	r, err := s.ImportOwned(ctx, obs, nil, "import")
	if err != nil {
		t.Fatal(err)
	}
	if r.ID != first.ID || r.Name != "Authored" || string(r.Metadata) != string(edited.Metadata) || string(r.Source.Definition) == string(first.Source.Definition) {
		t.Fatal("import overwrote authored content or lost observation")
	}
	if _, err = s.ImportOwned(ctx, Observation{Key: obs.Key, ObservedAt: at, Definition: first.Source.Definition}, nil, "import"); !errors.Is(err, ErrStaleObservation) {
		t.Fatal("stale import accepted", err)
	}
	obs.Deleted = true
	obs.ObservedAt = at.Add(2 * time.Second)
	r, err = s.ImportOwned(ctx, obs, nil, "import")
	if err != nil || r.Deleted() || r.Source.DeletedAt == nil {
		t.Fatal("source deletion deleted master", err)
	}
	r, err = s.SetDeleted(ctx, r.ID, r.Revision, true, "operator")
	if err != nil {
		t.Fatal(err)
	}
	obs.Deleted = false
	obs.ObservedAt = at.Add(3 * time.Second)
	r, err = s.ImportOwned(ctx, obs, nil, "import")
	if err != nil || !r.Deleted() {
		t.Fatal("remote restoration undid local deletion", err)
	}
	history, err := s.History(ctx, r.ID, 0, 100)
	if err != nil || history[0].Operation != "import_owned" || history[1].Operation != "patch" || history[len(history)-1].Record.Source.DeletedAt != nil {
		t.Fatal("import history lost", err)
	}
}

func TestExplicitCorrelationIsRevisionCheckedAndPreservesMaster(t *testing.T) {
	s, _, _ := open(t)
	master := create(t, s, Plan{Common: Common{Name: "Local plan"}, Status: "draft"})
	other := create(t, s, Plan{Common: Common{Name: "Local plan"}, Status: "draft"})
	obs := Observation{Key: SourceKey{SourceScope: SourceScope{Provider: "torque", Instance: "https://torque.test", Collection: "tasks", Kind: PlanKind}, ExternalID: "PLAN-1"}, ObservedAt: time.Now(), Definition: body(t, Plan{Common: Common{Name: "Remote plan"}, Status: "doing"})}
	if _, err := s.ImportOwned(ctx, obs, &Correlation{ID: master.ID, ExpectedRevision: 0}, "operator"); !errors.Is(err, ErrConflict) {
		t.Fatal("stale correlation accepted", err)
	}
	if _, err := s.LookupSource(ctx, obs.Key); !errors.Is(err, ErrNotFound) {
		t.Fatal("failed correlation left source", err)
	}
	r, err := s.ImportOwned(ctx, obs, &Correlation{ID: master.ID, ExpectedRevision: master.Revision}, "operator")
	if err != nil {
		t.Fatal(err)
	}
	if r.ID != master.ID || r.Name != master.Name || string(r.Local) != string(master.Local) || string(r.Metadata) != string(master.Metadata) {
		t.Fatal("correlation replaced master")
	}
	if _, err = s.ImportOwned(ctx, obs, &Correlation{ID: other.ID, ExpectedRevision: other.Revision}, "operator"); !errors.Is(err, ErrConflict) {
		t.Fatal("source assigned to two masters", err)
	}
	obs.Key.ExternalID = "PLAN-2"
	if _, err = s.ImportOwned(ctx, obs, &Correlation{ID: master.ID, ExpectedRevision: r.Revision}, "operator"); !errors.Is(err, ErrConflict) {
		t.Fatal("replaced an existing source identity", err)
	}
	// Generic source-owned imports cannot silently become masters.
	sourced := observe(t, s, obs)
	if _, err = s.ImportOwned(ctx, obs, nil, "operator"); !errors.Is(err, ErrConflict) {
		t.Fatal("implicit ownership promotion", err)
	}
	if _, err = s.TakeOwnership(ctx, sourced.ID, sourced.Revision, "operator"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ImportOwned(ctx, obs, nil, "operator"); err != nil {
		t.Fatal(err)
	}
}

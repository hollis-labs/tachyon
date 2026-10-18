package objects

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestExecutionLinksPinnedWorkflowQueriesAndHistory(t *testing.T) {
	s, _, _ := open(t)
	w := create(t, s, Workflow{Common: Common{Name: "Build"}})
	stream := create(t, s, Workstream{Common: Common{Name: "Fabric"}, Status: "active"})
	agent := create(t, s, Agent{Common: Common{Name: "Builder"}})
	task := create(t, s, Task{Common: Common{Name: "Work"}, Status: "todo"})
	run := create(t, s, Run{Common: Common{Name: "Attempt 1"}, Status: "planned", WorkflowID: w.ID, WorkflowRevision: w.Revision, ExecutionLinks: ExecutionLinks{WorkstreamID: stream.ID, TaskID: task.ID}})
	session := create(t, s, Session{Common: Common{Name: "Worker"}, Status: "running", RunID: run.ID, ExecutionLinks: ExecutionLinks{WorkstreamID: stream.ID, AgentID: agent.ID, TaskID: task.ID}})
	for _, f := range []ExecutionFilter{{Kind: RunKind, WorkflowID: w.ID}, {Kind: SessionKind, RunID: run.ID}, {Kind: SessionKind, Status: "running", AgentID: agent.ID, TaskID: task.ID}, {Kind: SessionKind, WorkstreamID: stream.ID}} {
		rows, err := s.ListExecutions(ctx, f)
		if err != nil || len(rows) != 1 {
			t.Fatal("missing execution", f, rows, err)
		}
	}
	if _, err := s.Patch(ctx, w.ID, w.Revision, Patch{Definition: json.RawMessage(`{"name":"New definition"}`)}, "operator"); err != nil {
		t.Fatal(err)
	}
	r, err := s.Get(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	var d Run
	if err = json.Unmarshal(r.Definition, &d); err != nil || d.WorkflowRevision != w.Revision {
		t.Fatal("run silently followed workflow edit", err)
	}
	for _, def := range []Definition{Run{Common: Common{Name: "Bad"}, Status: "planned", WorkflowID: w.ID}, Run{Common: Common{Name: "Bad"}, Status: "planned", WorkflowID: w.ID, WorkflowRevision: 999}, Session{Common: Common{Name: "Bad"}, Status: "running", RunID: task.ID}} {
		if _, err = s.Create(ctx, def, nil, "test"); !errors.Is(err, ErrInvalid) {
			t.Fatal("accepted invalid reference", err)
		}
	}
	end := time.Now().UTC()
	raw, _ := json.Marshal(map[string]any{"status": "completed", "ended_at": end})
	closed, err := s.Patch(ctx, session.ID, session.Revision, Patch{Definition: raw}, "operator")
	if err != nil || closed.Deleted() {
		t.Fatal("completion deleted session", err)
	}
	if _, err = s.Patch(ctx, session.ID, session.Revision, Patch{Definition: raw}, "stale"); !errors.Is(err, ErrConflict) {
		t.Fatal("lost update", err)
	}
	deleted, err := s.SetDeleted(ctx, run.ID, run.Revision, true, "operator")
	if err != nil || !deleted.Deleted() {
		t.Fatal(err)
	}
	rows, err := s.ListExecutions(ctx, ExecutionFilter{RunID: run.ID})
	if err != nil || len(rows) != 1 {
		t.Fatal("run deletion cascaded", err)
	}
	history, err := s.History(ctx, session.ID, 0, 100)
	if err != nil || len(history) != 2 {
		t.Fatal("execution history missing", err)
	}
	start := end.Add(time.Hour)
	if _, err = s.Create(ctx, Session{Common: Common{Name: "Bad dates"}, Status: "completed", StartedAt: &start, EndedAt: &end}, nil, "test"); !errors.Is(err, ErrInvalid) {
		t.Fatal("backwards window accepted", err)
	}
}

func TestExecutionLineageCyclesAcrossRelationsAndConcurrentEdits(t *testing.T) {
	s, _, _ := open(t)
	a := create(t, s, Session{Common: Common{Name: "A"}, Status: "unknown"})
	b := create(t, s, Session{Common: Common{Name: "B"}, Status: "running", ParentID: a.ID})
	if _, err := s.Patch(ctx, a.ID, a.Revision, Patch{Definition: json.RawMessage(fmt.Sprintf(`{"resumed_from_id":%q}`, b.ID))}, "test"); !errors.Is(err, ErrInvalid) {
		t.Fatal("mixed lineage cycle accepted", err)
	}
	r1 := create(t, s, Run{Common: Common{Name: "One"}, Status: "failed"})
	r2 := create(t, s, Run{Common: Common{Name: "Two"}, Status: "running"})
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, pair := range [][2]Record{{r1, r2}, {r2, r1}} {
		wg.Add(1)
		go func(a, b Record) {
			defer wg.Done()
			_, err := s.Patch(ctx, a.ID, a.Revision, Patch{Definition: json.RawMessage(fmt.Sprintf(`{"retry_of_id":%q}`, b.ID))}, "test")
			errs <- err
		}(pair[0], pair[1])
	}
	wg.Wait()
	close(errs)
	success, invalid := 0, 0
	for err := range errs {
		if err == nil {
			success++
		} else if errors.Is(err, ErrInvalid) {
			invalid++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || invalid != 1 {
		t.Fatal("concurrent cycle committed", success, invalid)
	}
}

func TestSessionIdentityStubEnrichmentNeverRegressesSource(t *testing.T) {
	s, db, _ := open(t)
	key := SourceKey{SourceScope: SourceScope{Provider: "tether", Instance: "https://hub", Collection: "sessions", Kind: SessionKind}, ExternalID: "same"}
	ensure := func(k SourceKey) Record {
		t.Helper()
		var r Record
		err := db.Writer.Submit(ctx, "test_identity", func(ctx context.Context, tx *sql.Tx) error {
			var err error
			r, err = EnsureSessionIdentityTx(ctx, tx, k, "test")
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	first := ensure(key)
	other := key
	other.Instance = "https://other"
	if ensure(other).ID == first.ID {
		t.Fatal("cross-instance collision")
	}
	at := time.Now().UTC().Add(-time.Minute)
	r := observe(t, s, Observation{Key: key, ObservedAt: at, Definition: body(t, Session{Common: Common{Name: "Observed"}, Status: "completed"})})
	if r.ID != first.ID || r.Revision != first.Revision+1 {
		t.Fatal("stub did not enrich", r)
	}
	r2 := ensure(key)
	if r2.ID != r.ID || r2.Revision != r.Revision || !r2.Source.ObservedAt.Equal(at) {
		t.Fatal("identity evidence regressed observation", r2)
	}
	deleted := observe(t, s, Observation{Key: key, ObservedAt: at.Add(time.Second), Deleted: true})
	if !ensure(key).Deleted() || ensure(key).Revision != deleted.Revision {
		t.Fatal("chat resurrected tombstone")
	}
	if _, err := s.UpdateOrCreate(ctx, Observation{Key: key, ObservedAt: at, Definition: r.Source.Definition}, "test"); !errors.Is(err, ErrStaleObservation) {
		t.Fatal("stale observation accepted", err)
	}
}

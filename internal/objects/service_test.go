package objects

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/hollis-labs/tachyon/internal/localstore"
)

var ctx = context.Background()

func open(t *testing.T) (*Service, *localstore.Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "tachyon.db")
	db, err := localstore.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return New(db), db, path
}
func create(t *testing.T, s *Service, d Definition) Record {
	t.Helper()
	r, err := s.Create(ctx, d, json.RawMessage(`{"operator":{"favorite":true}}`), "operator")
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func body(t *testing.T, d Definition) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func observe(t *testing.T, s *Service, o Observation) Record {
	t.Helper()
	r, err := s.UpdateOrCreate(ctx, o, "sync:test")
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func scope() SourceScope {
	return SourceScope{Provider: "tether", Instance: "hub-a", Collection: "catalog/agents", Kind: AgentKind}
}
func observation(t *testing.T, id string, at time.Time) Observation {
	return Observation{Key: SourceKey{SourceScope: scope(), ExternalID: id}, ObservedAt: at, Definition: body(t, Agent{Common: Common{Name: id}, Runtime: "claude", Enabled: true})}
}
func getAgent(t *testing.T, r Record) Agent {
	t.Helper()
	var d Agent
	if err := json.Unmarshal(r.Definition, &d); err != nil {
		t.Fatal(err)
	}
	return d
}

func TestTypedDefinitionsLinksAndHistory(t *testing.T) {
	s, _, _ := open(t)
	repo := create(t, s, Repository{Common: Common{Name: "Tachyon"}, Host: "github", Owner: "hollis-labs", Slug: "tachyon", DefaultBranch: "main"})
	project := create(t, s, Project{Common: Common{Name: "Control surface"}, RepositoryIDs: []string{repo.ID}})
	machine := create(t, s, Machine{Common: Common{Name: "Outpost"}, Platform: "linux", Environment: "dev"})
	agent := create(t, s, Agent{Common: Common{Name: "Builder"}, Runtime: "nanite", Enabled: true, ProjectID: project.ID, MachineID: machine.ID})
	workflow := create(t, s, Workflow{Common: Common{Name: "Review"}, ProjectID: project.ID, Steps: []WorkflowStep{{ID: "inspect", Verb: "scm.read"}, {ID: "review", Verb: "agent.run", DependsOn: []string{"inspect"}}}})
	team := create(t, s, Team{Common: Common{Name: "Delivery"}, Slots: []TeamSlot{{Name: "reviewer", Role: "review", AgentID: agent.ID, Max: 1}}, Coordinator: "reviewer", WorkflowID: workflow.ID})
	assignment := create(t, s, Assignment{Common: Common{Name: "Review Tachyon"}, TeamID: team.ID, Target: &Reference{ID: project.ID, Kind: ProjectKind}, State: "draft"})
	for _, r := range []Record{repo, project, machine, agent, workflow, team, assignment} {
		got, err := s.Get(ctx, r.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Kind != r.Kind || got.Ownership != "tachyon" || string(got.Definition) != string(r.Definition) {
			t.Fatalf("roundtrip changed %s", r.Kind)
		}
	}
	links, err := s.Links(ctx, team.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, l := range links {
		if l.Relation == "member" && l.Slot == "reviewer" && l.ToID == agent.ID {
			found = true
		}
	}
	if !found {
		t.Fatal("team membership link missing")
	}
	changed, err := s.Patch(ctx, team.ID, team.Revision, Patch{Definition: []byte(`{"slots":[],"coordinator":""}`)}, "operator")
	if err != nil {
		t.Fatal(err)
	}
	links, err = s.Links(ctx, team.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range links {
		if l.Relation == "member" {
			t.Fatal("removed membership survived")
		}
	}
	history, err := s.History(ctx, team.ID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	var original Team
	if err := json.Unmarshal(history[0].Record.Definition, &original); err != nil {
		t.Fatal(err)
	}
	if original.Slots[0].AgentID != agent.ID || history[len(history)-1].Record.Revision != changed.Revision {
		t.Fatal("revision history lost prior team definition")
	}
	// Local deletion does not cascade through assignments or remove history.
	deleted, err := s.SetDeleted(ctx, team.ID, changed.Revision, true, "operator")
	if err != nil {
		t.Fatal(err)
	}
	if !deleted.Deleted() {
		t.Fatal("delete not marked")
	}
	if _, err = s.Get(ctx, assignment.ID); err != nil {
		t.Fatal("assignment removed by team delete")
	}
	active, err := s.List(ctx, Filter{Kind: TeamKind})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range active {
		if r.ID == team.ID {
			t.Fatal("deleted team listed as active")
		}
	}
	restored, err := s.SetDeleted(ctx, team.ID, deleted.Revision, false, "operator")
	if err != nil || restored.Deleted() {
		t.Fatalf("restore: %v", err)
	}
}

func TestMergeRetainsLocalIntentAndSourceIdentity(t *testing.T) {
	s, _, _ := open(t)
	start := time.Now().UTC()
	o := observation(t, "agent-1", start)
	first := observe(t, s, o)
	authored, err := s.Patch(ctx, first.ID, first.Revision, Patch{Definition: []byte(`{"name":"My builder","properties":{"local":"yes"}}`), Metadata: []byte(`{"settings":{"color":"blue","priority":2}}`)}, "operator")
	if err != nil {
		t.Fatal(err)
	}
	o.ObservedAt = start.Add(time.Minute)
	o.Version = "opaque-sha"
	o.Definition = body(t, Agent{Common: Common{Name: "Renamed upstream", Properties: map[string]any{"upstream": "yes"}}, Runtime: "google-adk", Enabled: true})
	synced := observe(t, s, o)
	d := getAgent(t, synced)
	if synced.ID != first.ID || d.Name != "My builder" || d.Runtime != "google-adk" || d.Properties["local"] != "yes" || d.Properties["upstream"] != "yes" {
		t.Fatalf("merge lost identity or either side: %+v", d)
	}
	if string(synced.Metadata) != string(authored.Metadata) {
		t.Fatal("sync replaced local metadata")
	}
	cleared, err := s.Patch(ctx, synced.ID, synced.Revision, Patch{Definition: []byte(`{"name":null}`), Metadata: []byte(`{"settings":{"color":null}}`)}, "operator")
	if err != nil {
		t.Fatal(err)
	}
	if getAgent(t, cleared).Name != "Renamed upstream" || string(cleared.Metadata) != `{"settings":{"priority":2}}` {
		t.Fatal("null did not release override / remove metadata key")
	}
	o.ObservedAt = start.Add(2 * time.Minute)
	repeat := observe(t, s, o)
	if repeat.Revision != cleared.Revision {
		t.Fatal("unchanged poll invented history")
	}
	old := observation(t, "agent-1", start)
	if _, err = s.UpdateOrCreate(ctx, old, "sync:test"); !errors.Is(err, ErrStaleObservation) {
		t.Fatalf("old poll accepted: %v", err)
	}
	other := o
	other.Key.Instance = "hub-b"
	separate := observe(t, s, other)
	if separate.ID == first.ID {
		t.Fatal("identical remote IDs collided across instances")
	}
	if _, err = s.Patch(ctx, first.ID, authored.Revision, Patch{Definition: []byte(`{"name":"stale editor"}`)}, "operator"); !errors.Is(err, ErrConflict) {
		t.Fatalf("lost update allowed: %v", err)
	}
}

func TestConcurrentUpsertsAndEdits(t *testing.T) {
	s, _, _ := open(t)
	o := observation(t, "same-source", time.Now().UTC())
	var wg sync.WaitGroup
	records := make(chan Record, 16)
	errs := make(chan error, 16)
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := s.UpdateOrCreate(ctx, o, "sync")
			if err != nil {
				errs <- err
			} else {
				records <- r
			}
		}()
	}
	wg.Wait()
	close(records)
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	id := ""
	var baseline Record
	for r := range records {
		if id != "" && r.ID != id {
			t.Fatal("concurrent upsert created duplicate identity")
		}
		id = r.ID
		baseline = r
	}
	changed := make(chan Record, 2)
	conflicts := make(chan error, 2)
	for _, name := range []string{"first edit", "second edit"} {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			b, _ := json.Marshal(map[string]string{"name": name})
			r, err := s.Patch(ctx, id, baseline.Revision, Patch{Definition: b}, "operator")
			if err != nil {
				conflicts <- err
			} else {
				changed <- r
			}
		}(name)
	}
	wg.Wait()
	close(changed)
	close(conflicts)
	var winner Record
	for r := range changed {
		if winner.ID != "" {
			t.Fatal("two edits committed against one revision")
		}
		winner = r
	}
	if winner.ID == "" {
		t.Fatal("neither edit committed")
	}
	for err := range conflicts {
		if !errors.Is(err, ErrConflict) {
			t.Fatal(err)
		}
	}
	got, err := s.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != winner.Name {
		t.Fatal("losing edit leaked")
	}
	history, err := s.History(ctx, id, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if history[0].Operation != "observe" || history[len(history)-1].Record.Name != winner.Name {
		t.Fatal("history does not match committed writes")
	}
}

func TestReconcilePartialDeletionResurrectionAndWatermark(t *testing.T) {
	s, _, _ := open(t)
	start := time.Now().UTC()
	first := observe(t, s, observation(t, "one", start))
	second := observe(t, s, observation(t, "two", start))
	partial := start.Add(time.Minute)
	if _, err := s.Reconcile(ctx, scope(), partial, []Observation{observation(t, "one", partial)}, false, "sync"); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(ctx, second.ID)
	if err != nil || got.Deleted() {
		t.Fatal("partial inventory inferred deletion")
	}
	complete := partial.Add(time.Minute)
	if _, err = s.Reconcile(ctx, scope(), complete, []Observation{observation(t, "one", complete)}, true, "sync"); err != nil {
		t.Fatal(err)
	}
	got, err = s.Get(ctx, second.ID)
	if err != nil || !got.Deleted() {
		t.Fatal("complete inventory failed to retain tombstone")
	}
	history, err := s.History(ctx, second.ID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if history[len(history)-1].Operation != "source_delete" {
		t.Fatal("source deletion not recorded")
	}
	if _, err = s.UpdateOrCreate(ctx, observation(t, "two", partial), "sync"); !errors.Is(err, ErrStaleObservation) {
		t.Fatalf("stale record resurrected: %v", err)
	}
	if _, err = s.UpdateOrCreate(ctx, observation(t, "previously-unknown", partial), "sync"); !errors.Is(err, ErrStaleObservation) {
		t.Fatalf("old scan inserted unknown record: %v", err)
	}
	restored := observe(t, s, observation(t, "two", complete.Add(time.Minute)))
	if restored.ID != second.ID || restored.Deleted() {
		t.Fatal("remote reappearance lost identity")
	}
	local, err := s.SetDeleted(ctx, first.ID, first.Revision, true, "operator")
	if err != nil {
		t.Fatal(err)
	}
	updated := observe(t, s, observation(t, "one", complete.Add(time.Minute)))
	if !updated.Deleted() || updated.DeletedAt == nil || !updated.DeletedAt.Equal(*local.DeletedAt) {
		t.Fatal("poll undid local deletion")
	}
	empty := complete.Add(2 * time.Minute)
	if _, err = s.Reconcile(ctx, scope(), empty, nil, true, "sync"); err != nil {
		t.Fatal(err)
	}
	rows, err := s.List(ctx, Filter{Kind: AgentKind})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if r.Source != nil && r.Source.Key.SourceScope == scope() {
			t.Fatal("complete empty collection left active source records")
		}
	}
}

func TestInvalidBatchRollsBackAndReferencesAreTyped(t *testing.T) {
	s, _, _ := open(t)
	at := time.Now().UTC()
	good := observation(t, "good", at)
	bad := observation(t, "bad", at)
	bad.Definition = body(t, Agent{Common: Common{Name: "bad"}, ProjectID: "missing-project"})
	if _, err := s.Reconcile(ctx, scope(), at, []Observation{good, bad}, true, "sync"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("dangling reference accepted: %v", err)
	}
	if _, err := s.LookupSource(ctx, good.Key); !errors.Is(err, ErrNotFound) {
		t.Fatalf("failed batch partially committed: %v", err)
	}
	// Failed scan did not advance the collection watermark.
	observe(t, s, observation(t, "good", at.Add(-time.Minute)))
	machine := create(t, s, Machine{Common: Common{Name: "machine"}})
	if _, err := s.Create(ctx, Agent{Common: Common{Name: "wrong kind"}, ProjectID: machine.ID}, nil, "operator"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("wrong-kind reference accepted: %v", err)
	}
	if _, err := s.Create(ctx, Workflow{Common: Common{Name: "cycle"}, Steps: []WorkflowStep{{ID: "a", Verb: "run", DependsOn: []string{"b"}}, {ID: "b", Verb: "run", DependsOn: []string{"a"}}}}, nil, "operator"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("cycle accepted: %v", err)
	}
	agent := create(t, s, Agent{Common: Common{Name: "valid"}})
	if _, err := s.Patch(ctx, agent.ID, agent.Revision, Patch{Definition: []byte(`{"made_up_field":true}`)}, "operator"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unknown typed field accepted: %v", err)
	}
	same, err := s.Get(ctx, agent.ID)
	if err != nil || same.Revision != agent.Revision {
		t.Fatal("failed validation advanced revision")
	}
}

func TestHistoryPersistsAlongsideLegacyData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tachyon.db")
	db, err := localstore.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	err = db.Writer.Submit(ctx, "seed_legacy", func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO app_settings(key,value) VALUES('legacy','keep'); INSERT INTO api_cache(key,scope,identity,body,content_type,updated_at) VALUES('old','forge','identity','{}','application/json',1)`)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	s := New(db)
	r := create(t, s, Repository{Common: Common{Name: "Durable repo"}, Owner: "org", Slug: "repo"})
	// Ordinary cache eviction must never delete domain objects or history.
	err = db.Writer.Submit(ctx, "evict_cache", func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "DELETE FROM api_cache")
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = localstore.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s = New(db)
	got, err := s.Get(ctx, r.ID)
	if err != nil || got.Name != r.Name {
		t.Fatal("durable data lost on reopen")
	}
	history, err := s.History(ctx, r.ID, 0, 100)
	if err != nil || history[0].Record.ID != r.ID {
		t.Fatal("durable history lost")
	}
	var value string
	if err = db.Reader.QueryRow("SELECT value FROM app_settings WHERE key='legacy'").Scan(&value); err != nil || value != "keep" {
		t.Fatal("legacy settings changed")
	}
}

func TestExplicitMasterOwnershipKeepsObservationSeparate(t *testing.T) {
	s, _, _ := open(t)
	start := time.Now().UTC()
	o := observation(t, "adopted", start)
	r := observe(t, s, o)
	master, err := s.TakeOwnership(ctx, r.ID, r.Revision, "operator")
	if err != nil {
		t.Fatal(err)
	}
	o.ObservedAt = start.Add(time.Minute)
	o.Definition = body(t, Agent{Common: Common{Name: "Upstream renamed"}, Runtime: "changed"})
	changed := observe(t, s, o)
	if changed.Name != master.Name || getAgent(t, changed).Runtime != getAgent(t, master).Runtime {
		t.Fatal("observation changed master definition")
	}
	if string(changed.Source.Definition) == string(master.Source.Definition) {
		t.Fatal("master lost source observation")
	}
	o.ObservedAt = start.Add(2 * time.Minute)
	o.Deleted = true
	o.Definition = nil
	deleted := observe(t, s, o)
	if deleted.Deleted() || deleted.Source.DeletedAt == nil {
		t.Fatal("source deletion deleted the master or lost provenance")
	}
	records, err := s.List(ctx, Filter{Kind: AgentKind})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, record := range records {
		if record.ID == master.ID {
			found = true
		}
	}
	if !found {
		t.Fatal("master hidden after source deletion")
	}
}

func TestRetryLocalCreateAndPagedHistory(t *testing.T) {
	s, _, _ := open(t)
	id := "0192a000-0000-7000-8000-000000000001"
	d := Project{Common: Common{Name: "Local master"}}
	first, err := s.CreateWithID(ctx, id, d, nil, "operator")
	if err != nil {
		t.Fatal(err)
	}
	repeated, err := s.CreateWithID(ctx, id, d, nil, "operator")
	if err != nil || repeated.Revision != first.Revision {
		t.Fatalf("retry changed creation: %v", err)
	}
	different := d
	different.Name = "different"
	if _, err = s.CreateWithID(ctx, id, different, nil, "operator"); !errors.Is(err, ErrConflict) {
		t.Fatalf("identity reuse overwrote local record: %v", err)
	}
	next, err := s.Patch(ctx, id, first.Revision, Patch{Metadata: []byte(`{"favorite":true}`)}, "operator")
	if err != nil {
		t.Fatal(err)
	}
	page, err := s.History(ctx, id, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	tail, err := s.History(ctx, id, page[0].Record.Revision, 1)
	if err != nil || tail[0].Record.Revision != next.Revision {
		t.Fatal("revision cursor skipped or repeated history")
	}
	second := create(t, s, Project{Common: Common{Name: "Other"}})
	records, err := s.List(ctx, Filter{Kind: ProjectKind, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	rest, err := s.List(ctx, Filter{Kind: ProjectKind, Limit: 1, AfterID: records[0].ID})
	if err != nil {
		t.Fatal(err)
	}
	if records[0].ID == rest[0].ID || !(records[0].ID == id && rest[0].ID == second.ID || records[0].ID == second.ID && rest[0].ID == id) {
		t.Fatal("list cursor lost stable identities")
	}
}

func TestEquivalentSourceJSONDoesNotInventRevisions(t *testing.T) {
	s, _, _ := open(t)
	o := observation(t, "canonical", time.Now().UTC())
	o.Definition = []byte(`{"name":"canonical","properties":{"a":1,"b":{"x":2,"y":3}}}`)
	r := observe(t, s, o)
	o.ObservedAt = o.ObservedAt.Add(time.Second)
	o.Definition = []byte(`{"properties":{"b":{"y":3,"x":2},"a":1},"name":"canonical"}`)
	repeat := observe(t, s, o)
	if repeat.Revision != r.Revision {
		t.Fatal("JSON ordering invented history")
	}
}

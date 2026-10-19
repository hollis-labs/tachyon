package provideractions

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hollis-labs/tachyon/internal/localstore"
	"github.com/hollis-labs/tachyon/internal/objects"
)

var ctx = context.Background()

type fakeBound struct {
	identity string
	calls    atomic.Int32
	form     func(string, json.RawMessage) (json.RawMessage, error)
}

func (f *fakeBound) Target() string   { return "https://tether.test" }
func (f *fakeBound) Identity() string { return f.identity }
func (f *fakeBound) Form(_ context.Context, key string, raw json.RawMessage) (json.RawMessage, error) {
	f.calls.Add(1)
	if f.form != nil {
		return f.form(key, raw)
	}
	return json.RawMessage(`{"run":{"id":"run-1"}}`), nil
}
func fixture(t *testing.T) (*Service, objects.Record, *fakeBound) {
	t.Helper()
	db, e := localstore.Open(ctx, filepath.Join(t.TempDir(), "db.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close() })
	var preview any
	if e = json.Unmarshal([]byte(`{"id":"delivery","name":"Delivery","version":1,"slots":[{"name":"coordinator","role":"owner","definition_ref":{"id":"owner","revision":"v1","digest":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},"resolution":"fresh","activation":"singleton","required":false,"min":0,"max":1},{"name":"worker","role":"work","definition_ref":{"id":"worker","revision":"v1","digest":"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"},"resolution":"fresh","activation":"singleton","required":true,"min":1,"max":1}],"phases":[{"id":"work","kind":"flex","active_slots":["coordinator","worker"],"owner_slot":"coordinator","exit_trigger":{"kind":"manual","spec":{"signal":"done"}}}],"authority":{"mode":"strict","grants":[{"from_slot":"coordinator","verb":"may_assign","to_slot":"worker"}]},"routing":{"coordinator_slot":"coordinator","rules":[]},"policy":{"history":"none","spawn":{"max_depth":1,"max_children":4}}}`), &preview); e != nil {
		t.Fatal(e)
	}
	row, e := objects.New(db).Create(ctx, objects.Team{Common: objects.Common{Name: "Delivery", Properties: map[string]any{"builder_team": map[string]any{"preview": preview}}}, Coordinator: "coordinator", Slots: []objects.TeamSlot{{Name: "coordinator", Role: "owner", Min: 0, Max: 1}, {Name: "worker", Role: "work", Min: 1, Max: 1}}}, nil, "test")
	if e != nil {
		t.Fatal(e)
	}
	f := &fakeBound{identity: "credential-a"}
	return &Service{Store: db, Bind: func() (Bound, error) { return f, nil }}, row, f
}
func prepare(t *testing.T, s *Service, row objects.Record) Receipt {
	t.Helper()
	r, e := s.Prepare(ctx, PrepareRequest{ID: uuid.NewString(), ObjectID: row.ID, ExpectedRevision: row.Revision, Mission: "Review this implementation"}, "operator:test")
	if e != nil {
		t.Fatal(e)
	}
	return r
}
func TestReviewedApplyAndPersistentReceipt(t *testing.T) {
	s, row, f := fixture(t)
	r := prepare(t, s, row)
	if f.calls.Load() != 0 {
		t.Fatal("preview dispatched")
	}
	var request struct {
		Team struct {
			Slots []struct {
				Workspace map[string]string `json:"workspace"`
			} `json:"slots"`
		} `json:"team"`
	}
	if json.Unmarshal(r.Payload, &request) != nil || request.Team.Slots[1].Workspace["mission"] != "Review this implementation" {
		t.Fatal("mission not pinned")
	}
	f.form = func(key string, raw json.RawMessage) (json.RawMessage, error) {
		if key != "tachyon-"+r.ID || string(raw) != string(r.Payload) {
			t.Error("unpinned request")
		}
		return json.RawMessage(`{"run":{"id":"r1"}}`), nil
	}
	result, e := s.Apply(ctx, ApplyRequest{ID: r.ID, Digest: r.Digest}, "operator:test")
	if e != nil || result.State != "succeeded" {
		t.Fatal(result, e)
	}
	// New service instance, same durable store; success never sends a second time.
	restarted := &Service{Store: s.Store, Bind: s.Bind}
	result, e = restarted.Apply(ctx, ApplyRequest{ID: r.ID, Digest: r.Digest}, "operator:test")
	if e != nil || result.State != "succeeded" || f.calls.Load() != 1 {
		t.Fatal(result, e, f.calls.Load())
	}
	page, e := restarted.List(ctx, ListRequest{ObjectID: row.ID})
	if e != nil || len(page.Items) != 1 || page.Items[0].State != "succeeded" {
		t.Fatal(page, e)
	}
}
func TestRevisionRetirementAndDigestGates(t *testing.T) {
	for _, mode := range []string{"revision", "retired", "digest", "expired", "credential", "actor"} {
		t.Run(mode, func(t *testing.T) {
			s, row, f := fixture(t)
			r := prepare(t, s, row)
			in := ApplyRequest{ID: r.ID, Digest: r.Digest}
			actor := "operator:test"
			switch mode {
			case "revision":
				_, e := objects.New(s.Store).Patch(ctx, row.ID, row.Revision, objects.Patch{Definition: json.RawMessage(`{"name":"Changed"}`)}, "test")
				if e != nil {
					t.Fatal(e)
				}
			case "retired":
				_, e := objects.New(s.Store).SetDeleted(ctx, row.ID, row.Revision, true, "test")
				if e != nil {
					t.Fatal(e)
				}
			case "digest":
				in.Digest = "wrong"
			case "credential":
				f.identity = "credential-b"
			case "actor":
				actor = ""
			case "expired":
				if e := s.Store.Writer.Submit(ctx, "test", func(ctx context.Context, tx *sql.Tx) error {
					_, e := tx.ExecContext(ctx, "UPDATE provider_actions SET created_at=? WHERE id=?", time.Now().Add(-time.Hour).Format(time.RFC3339Nano), r.ID)
					return e
				}); e != nil {
					t.Fatal(e)
				}
			}
			if _, e := s.Apply(ctx, in, actor); e == nil || f.calls.Load() != 0 {
				t.Fatal("gate failed", e, f.calls.Load())
			}
		})
	}
}
func TestLostResponseRequiresExplicitSameRequestRecovery(t *testing.T) {
	s, row, f := fixture(t)
	r := prepare(t, s, row)
	keys := []string{}
	payloads := []string{}
	f.form = func(key string, raw json.RawMessage) (json.RawMessage, error) {
		keys = append(keys, key)
		payloads = append(payloads, string(raw))
		if len(keys) == 1 {
			return nil, errors.New("lost response")
		}
		return json.RawMessage(`{"run":{"id":"r1"}}`), nil
	}
	result, e := s.Apply(ctx, ApplyRequest{ID: r.ID, Digest: r.Digest}, "operator:test")
	if e != nil || result.State != "unknown" {
		t.Fatal(result, e)
	}
	if _, e = s.Apply(ctx, ApplyRequest{ID: r.ID, Digest: r.Digest}, "operator:test"); e == nil || f.calls.Load() != 1 {
		t.Fatal("blind retry")
	}
	// Edits after a send must not replace the retained payload during recovery.
	if _, e = objects.New(s.Store).Patch(ctx, row.ID, row.Revision, objects.Patch{Definition: json.RawMessage(`{"name":"Later edit"}`)}, "test"); e != nil {
		t.Fatal(e)
	}
	result, e = s.Apply(ctx, ApplyRequest{ID: r.ID, Digest: r.Digest, Retry: true}, "operator:test")
	if e != nil || result.State != "succeeded" || len(keys) != 2 || keys[0] != keys[1] || payloads[0] != payloads[1] {
		t.Fatal(result, e, keys)
	}
}
func TestConcurrentApplyAndAbandonedClaim(t *testing.T) {
	s, row, f := fixture(t)
	r := prepare(t, s, row)
	entered := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	f.form = func(_ string, _ json.RawMessage) (json.RawMessage, error) {
		close(entered)
		<-release
		return json.RawMessage(`{}`), nil
	}
	go func() { _, e := s.Apply(ctx, ApplyRequest{ID: r.ID, Digest: r.Digest}, "operator:test"); done <- e }()
	<-entered
	_, err := s.Apply(ctx, ApplyRequest{ID: r.ID, Digest: r.Digest, Retry: true}, "operator:test")
	close(release)
	if err == nil {
		t.Fatal("concurrent send admitted")
	}
	if e := <-done; e != nil {
		t.Fatal(e)
	}
	if f.calls.Load() != 1 {
		t.Fatal("duplicate provider call")
	}
	// Simulate a crashed host after recording intent but before recording outcome.
	if e := s.Store.Writer.Submit(ctx, "test", func(ctx context.Context, tx *sql.Tx) error {
		_, e := tx.ExecContext(ctx, "UPDATE provider_actions SET state='sending',lease_until=0 WHERE id=?", r.ID)
		return e
	}); e != nil {
		t.Fatal(e)
	}
	f.form = nil
	if _, e := s.Apply(ctx, ApplyRequest{ID: r.ID, Digest: r.Digest}, "operator:test"); e == nil {
		t.Fatal("crash caused automatic replay")
	}
	result, e := s.Apply(ctx, ApplyRequest{ID: r.ID, Digest: r.Digest, Retry: true}, "operator:test")
	if e != nil || result.State != "succeeded" {
		t.Fatal(result, e)
	}
}
func TestPreviewRetainsIDAndRejectsStaleSecondaryConfiguration(t *testing.T) {
	s, row, f := fixture(t)
	in := PrepareRequest{ID: uuid.NewString(), ObjectID: row.ID, ExpectedRevision: row.Revision, Mission: "Review"}
	first, e := s.Prepare(ctx, in, "operator:test")
	if e != nil {
		t.Fatal(e)
	}
	second, e := s.Prepare(ctx, in, "operator:test")
	if e != nil || first.Digest != second.Digest {
		t.Fatal(second, e)
	}
	in.Mission = "Different"
	if _, e = s.Prepare(ctx, in, "operator:test"); e == nil {
		t.Fatal("preview key reused for different mission")
	}
	row, e = objects.New(s.Store).Patch(ctx, row.ID, row.Revision, objects.Patch{Definition: json.RawMessage(`{"name":"Changed native name"}`)}, "test")
	if e != nil {
		t.Fatal(e)
	}
	in.ID = uuid.NewString()
	in.ExpectedRevision = row.Revision
	if _, e = s.Prepare(ctx, in, "operator:test"); e == nil || f.calls.Load() != 0 {
		t.Fatal("stale preview accepted", e)
	}
}

func TestDifferentMissionsDoNotOverwriteImmutableProviderDefinition(t *testing.T) {
	s, row, _ := fixture(t)
	first := prepare(t, s, row)
	second, err := s.Prepare(ctx, PrepareRequest{ID: uuid.NewString(), ObjectID: row.ID, ExpectedRevision: row.Revision, Mission: "A different mission"}, "operator:test")
	if err != nil {
		t.Fatal(err)
	}
	var a, b struct {
		Team struct {
			ID      string `json:"id"`
			Version int    `json:"version"`
		} `json:"team"`
	}
	if json.Unmarshal(first.Payload, &a) != nil || json.Unmarshal(second.Payload, &b) != nil || a.Team.ID == b.Team.ID || a.Team.ID != "tachyon-"+row.ID+"-"+first.ID || a.Team.Version != b.Team.Version {
		t.Fatal("formation reused immutable definition", a, b)
	}
	current, err := objects.New(s.Store).Get(ctx, row.ID)
	if err != nil || string(current.Definition) != string(row.Definition) || current.Revision != row.Revision {
		t.Fatal("preview edited saved definition", err)
	}
}

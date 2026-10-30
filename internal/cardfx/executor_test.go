package cardfx

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/hollis-labs/libs/mesh/core/hitl"
	"github.com/hollis-labs/tachyon/internal/approvals"
	"github.com/hollis-labs/tachyon/internal/cards"
	"github.com/hollis-labs/tachyon/internal/localstore"
)

type fakeTarget struct {
	applied []Effect
	failOn  string
}

func (*fakeTarget) Scheme() string { return "fake" }
func (*fakeTarget) Validate(e Effect) error {
	if e.Op != "set" {
		return errors.New("op must be set")
	}
	return nil
}
func (f *fakeTarget) Apply(_ context.Context, e Effect) (string, error) {
	if e.Target == f.failOn {
		return "", errors.New("boom")
	}
	f.applied = append(f.applied, e)
	return "done " + e.Target, nil
}

func setup(t *testing.T) (*approvals.Inbox, *Store, *approvals.Store) {
	t.Helper()
	db, err := localstore.Open(context.Background(), filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	items := &approvals.Store{DB: db}
	return approvals.NewInbox(items, nil), &Store{DB: db}, items
}

func ask(t *testing.T, inbox *approvals.Inbox, key string, effects any, mutate func(*hitl.EnqueueRequest)) string {
	t.Helper()
	req := hitl.EnqueueRequest{ContractVersion: hitl.ContractVersion, Kind: "card.approval", IdempotencyKey: key, Source: hitl.SourceAssertion{AgentID: "planner-1"}, Extra: map[string]json.RawMessage{}}
	for k, v := range map[string]any{"title": "Apply", "data": map[string]any{"n": 1}, "effects": effects} {
		req.Extra[k], _ = json.Marshal(v)
	}
	if err := approvals.CardRegistry.Prepare(&req, "service:planner"); err != nil {
		t.Fatal(err)
	}
	if mutate != nil {
		mutate(&req)
	}
	h, err := inbox.Service.Enqueue(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	return h.ItemID
}

func answer(t *testing.T, inbox *approvals.Inbox, id, decision string) {
	t.Helper()
	rev := int64(1)
	who := hitl.Participant{PrincipalRef: "browser-session:op", Authority: "tachyon-inbox", Assurance: "operator-token", Responder: &hitl.Responder{Kind: "human-session", Ref: "browser-session:op"}}
	if _, err := inbox.Service.Respond(context.Background(), hitl.RespondCommand{ItemID: id, ExpectedRevision: &rev, Response: hitl.Response{Kind: "card.approval", Decision: decision, Note: "ship it"}, Participant: who}); err != nil {
		t.Fatal(err)
	}
}

func TestApprovedEffectsRunOnceAndRecordAReceipt(t *testing.T) {
	inbox, store, items := setup(t)
	ft := &fakeTarget{}
	ex := New(items, store, ft)
	ctx := context.Background()
	id := ask(t, inbox, "k1", []map[string]any{{"target": "fake:a", "op": "set", "value": "1"}, {"target": "fake:b", "op": "set"}}, nil)

	if err := ex.Run(ctx); err != nil || len(ft.applied) != 0 {
		t.Fatalf("an unanswered card must not run: %v %v", err, ft.applied)
	}
	answer(t, inbox, id, "approved")
	if err := ex.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if err := ex.Run(ctx); err != nil || len(ft.applied) != 2 {
		t.Fatalf("effects must run exactly once: %v %v", err, ft.applied)
	}
	r, err := store.Get(ctx, id)
	if err != nil || r == nil || r.State != StateCompleted || len(r.Effects) != 2 || r.Approver != "browser-session:op" || r.Note != "ship it" || r.Assurance != "operator-token" {
		t.Fatalf("%+v %v", r, err)
	}
}

func TestDeniedCardsAndCardsWithoutEffectsNeverRun(t *testing.T) {
	inbox, store, items := setup(t)
	ft := &fakeTarget{}
	ex := New(items, store, ft)
	id := ask(t, inbox, "k2", []map[string]any{{"target": "fake:a", "op": "set"}}, nil)
	answer(t, inbox, id, "denied")
	if err := ex.Run(context.Background()); err != nil || len(ft.applied) != 0 {
		t.Fatalf("%v %v", err, ft.applied)
	}
	if r, _ := store.Get(context.Background(), id); r == nil || r.State != StateNone || r.Decision != "denied" {
		t.Fatalf("%+v", r)
	}
}

func TestRefusesTamperedDigestUnknownTargetAndInvalidEffects(t *testing.T) {
	inbox, store, items := setup(t)
	ft := &fakeTarget{}
	ex := New(items, store, ft)
	ctx := context.Background()
	cases := map[string]struct {
		effects []map[string]any
		mutate  func(*hitl.EnqueueRequest)
		reason  string
	}{
		"tampered digest": {[]map[string]any{{"target": "fake:a", "op": "set"}}, func(r *hitl.EnqueueRequest) { r.Extra["op_digest"] = json.RawMessage(`"sha256:00"`) }, "digest"},
		"unknown scheme":  {[]map[string]any{{"target": "shell:rm", "op": "set"}}, nil, "not allowed"},
		"one bad effect":  {[]map[string]any{{"target": "fake:a", "op": "set"}, {"target": "fake:b", "op": "drop"}}, nil, "Effect 2"},
	}
	i := 0
	for name, c := range cases {
		i++
		id := ask(t, inbox, "bad"+string(rune('a'+i)), c.effects, c.mutate)
		answer(t, inbox, id, "approved")
		if err := ex.Run(ctx); err != nil {
			t.Fatal(err)
		}
		r, _ := store.Get(ctx, id)
		if r == nil || r.State != StateRefused || !strings.Contains(r.Reason, c.reason) {
			t.Fatalf("%s: %+v", name, r)
		}
	}
	if len(ft.applied) != 0 {
		t.Fatalf("refused sets must run nothing, applied %v", ft.applied)
	}
}

func TestFailureSkipsTheRestAndRecoveryDoesNotRetry(t *testing.T) {
	inbox, store, items := setup(t)
	ft := &fakeTarget{failOn: "fake:b"}
	ex := New(items, store, ft)
	ctx := context.Background()
	id := ask(t, inbox, "k3", []map[string]any{{"target": "fake:a", "op": "set"}, {"target": "fake:b", "op": "set"}, {"target": "fake:c", "op": "set"}}, nil)
	answer(t, inbox, id, "approved")
	if err := ex.Run(ctx); err != nil {
		t.Fatal(err)
	}
	r, _ := store.Get(ctx, id)
	if r.State != StateFailed || r.Effects[0].Status != "ok" || r.Effects[1].Status != "failed" || r.Effects[2].Status != "skipped" || len(ft.applied) != 1 {
		t.Fatalf("%+v %v", r, ft.applied)
	}
	// A run a crash left in flight is closed, never replayed.
	id2 := ask(t, inbox, "k4", []map[string]any{{"target": "fake:a", "op": "set"}}, nil)
	answer(t, inbox, id2, "approved")
	if won, err := store.Claim(ctx, id2, ex.now()); err != nil || !won {
		t.Fatal(won, err)
	}
	if err := ex.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	if err := ex.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if r, _ := store.Get(ctx, id2); r.State != StateFailed || !strings.Contains(r.Reason, "Interrupted") || len(ft.applied) != 1 {
		t.Fatalf("%+v %v", r, ft.applied)
	}
}

func TestTorqueTargetValidatesAndPostsATransition(t *testing.T) {
	var hits atomic.Int32
	var gotPath, gotAuth, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		gotPath, gotAuth = r.URL.Path, r.Header.Get("Authorization")
		b := make([]byte, 200)
		n, _ := r.Body.Read(b)
		gotBody = string(b[:n])
		if strings.Contains(r.URL.Path, "0099") {
			w.WriteHeader(422)
			_, _ = w.Write([]byte(`{"error":"invalid transition"}`))
			return
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	tq := NewTorque(srv.URL, "tok")
	for _, bad := range []Effect{
		{Target: "torque:task/../x", Op: "status", Value: "done"},
		{Target: "torque:task/CW-20261023-0001", Op: "delete", Value: "done"},
		{Target: "torque:task/CW-20261023-0001", Op: "status", Value: "Done; drop"},
	} {
		if tq.Validate(bad) == nil {
			t.Fatalf("must refuse %+v", bad)
		}
	}
	detail, err := tq.Apply(context.Background(), Effect{Target: "torque:task/CW-20261023-0001", Op: "status", Value: "done"})
	if err != nil || detail != "CW-20261023-0001 set to done" || gotPath != "/api/v1/tasks/CW-20261023-0001/transition" || gotAuth != "Bearer tok" || !strings.Contains(gotBody, `"status":"done"`) || strings.Contains(gotBody, "force") {
		t.Fatalf("%q %v %s %s %s", detail, err, gotPath, gotAuth, gotBody)
	}
	if _, err := tq.Apply(context.Background(), Effect{Target: "torque:task/CW-20261023-0099", Op: "status", Value: "done"}); err == nil || !strings.Contains(err.Error(), "invalid transition") {
		t.Fatalf("a Torque refusal must surface: %v", err)
	}
}

func TestApprovedAlternativeRunsInsteadOfTheMainEffects(t *testing.T) {
	inbox, store, items := setup(t)
	ft := &fakeTarget{}
	ex := New(items, store, ft)
	ctx := context.Background()
	alt := []map[string]any{{"id": "archive", "label": "Archive instead", "effects": []map[string]any{{"target": "fake:archived", "op": "set"}}}}
	mk := func(key string) string {
		return ask(t, inbox, key, []map[string]any{{"target": "fake:doomed", "op": "set"}}, func(r *hitl.EnqueueRequest) {
			r.Extra["alternatives"], _ = json.Marshal(alt)
			d, _ := cards.DigestOf(r.Kind, r.Extra)
			r.Extra["op_digest"], _ = json.Marshal(d)
		})
	}
	choose := func(id, alternative string) {
		rev := int64(1)
		who := hitl.Participant{PrincipalRef: "browser-session:op", Authority: "tachyon-inbox", Assurance: "operator-token", Responder: &hitl.Responder{Kind: "human-session", Ref: "browser-session:op"}}
		resp := hitl.Response{Kind: "card.approval", Decision: "approved", Extra: map[string]json.RawMessage{}}
		if alternative != "" {
			resp.Extra["alternative_id"], _ = json.Marshal(alternative)
		}
		if _, err := inbox.Service.Respond(ctx, hitl.RespondCommand{ItemID: id, ExpectedRevision: &rev, Response: resp, Participant: who}); err != nil {
			t.Fatal(err)
		}
	}
	id := mk("alt1")
	choose(id, "archive")
	if err := ex.Run(ctx); err != nil || len(ft.applied) != 1 || ft.applied[0].Target != "fake:archived" {
		t.Fatalf("only the alternative must run: %v %v", err, ft.applied)
	}
	if r, _ := store.Get(ctx, id); r == nil || r.State != StateCompleted || r.Alternative != "archive" {
		t.Fatalf("%+v", r)
	}
	// An alternative that is not in the approved request is refused, nothing runs.
	ft.applied = nil
	id = mk("alt2")
	choose(id, "nuke")
	if err := ex.Run(ctx); err != nil || len(ft.applied) != 0 {
		t.Fatalf("%v %v", err, ft.applied)
	}
	if r, _ := store.Get(ctx, id); r == nil || r.State != StateRefused {
		t.Fatalf("%+v", r)
	}
	// Without an alternative the main effects run.
	id = mk("alt3")
	choose(id, "")
	_ = ex.Run(ctx)
	if len(ft.applied) != 1 || ft.applied[0].Target != "fake:doomed" {
		t.Fatalf("%v", ft.applied)
	}
}

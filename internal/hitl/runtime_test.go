package hitl

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	core "github.com/hollis-labs/substrate/mesh/hitl"
	"github.com/hollis-labs/tachyon/internal/contract"
)

type fakeClient struct {
	mu       sync.Mutex
	requests []EnqueueRequest
	result   core.RetrievalResult
	err      error
}

func (f *fakeClient) Enqueue(_ context.Context, r EnqueueRequest) (EnqueueHandle, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, r)
	return EnqueueHandle{ContractVersion: "1.0", ItemID: fmt.Sprint(len(f.requests)), State: "submitted", Revision: 1, ItemURL: "/hitl/items/1"}, f.err
}
func (f *fakeClient) Retrieve(context.Context, string, int) (core.RetrievalResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.result, f.err
}
func ask(t *testing.T, r *Runtime, payload string, current func() bool) *contract.AskDetail {
	t.Helper()
	b := r.Attach(context.Background(), "work_write", []byte(payload), "generation", current, []byte(`{"status":"ask","ask":{"prompt":"Proceed?","options":["yes","no"],"context":{"hello":"world"},"item_id":"forged","state":"approved"}}`))
	var e contract.ResultEnvelope
	if json.Unmarshal(b, &e) != nil {
		t.Fatal(string(b))
	}
	if e.Status != "ask" || e.Ask.Continuation != "unavailable" || len(e.Ask.Options) != 2 || e.Ask.Context["hello"] != "world" {
		t.Fatal(string(b))
	}
	return e.Ask
}
func terminal(f *fakeClient, state core.State, kind, decision string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	snapshot, _ := json.Marshal(f.requests[0])
	now := time.Now().UTC()
	var o core.Outcome
	switch state {
	case core.StateResolved:
		o = core.Resolved{ItemID: "1", InteractionRevision: 2, Resolution: core.ResolutionRecord{ResolutionID: "res", Response: core.Response{Kind: kind, Decision: decision}, Participant: core.Participant{PrincipalRef: "operator", Assurance: "local"}, ResolvedAt: now, InteractionRevision: 2}}
	case core.StateCanceled:
		o = core.Canceled{ItemID: "1", InteractionRevision: 2, Cause: core.CauseParticipantCanceled, TerminatedAt: now}
	case core.StateExpired:
		o = core.Expired{ItemID: "1", InteractionRevision: 2, TerminatedAt: now}
	case core.StateFailed:
		o = core.Failed{ItemID: "1", InteractionRevision: 2, ErrorCode: "failure", Message: "failed", TerminatedAt: now}
	case core.StateSuperseded:
		o = core.Superseded{ItemID: "1", InteractionRevision: 2, ReplacementItemID: "other", TerminatedAt: now}
	}
	f.result = core.RetrievalResult{ContractVersion: "1.0", Mode: "get", WaitStatus: "not_waited", RetrievedAt: now, Item: core.ItemView{ContractVersion: "1.0", ItemID: "1", State: state, Revision: 2, RequestSnapshot: snapshot, EnqueuedAt: now, UpdatedAt: now, TerminalOutcome: o}}
}
func TestAuthoritativeOutcomes(t *testing.T) {
	for _, tc := range []struct {
		state          core.State
		kind, decision string
		approved       bool
	}{{core.StateResolved, "approval", "approved", true}, {core.StateResolved, "approval", "denied", false}, {core.StateResolved, "attention", "approved", false}, {core.StateCanceled, "", "", false}, {core.StateExpired, "", "", false}, {core.StateFailed, "", "", false}, {core.StateSuperseded, "", "", false}} {
		t.Run(string(tc.state)+tc.kind+tc.decision, func(t *testing.T) {
			f := &fakeClient{}
			r, _ := NewRuntime(f, "http://127.0.0.1:18199")
			a := ask(t, r, `{"amount":9007199254740993}`, func() bool { return true })
			terminal(f, tc.state, tc.kind, tc.decision)
			s, e := r.Status(context.Background(), a.OperationID, 0)
			if tc.kind == "attention" {
				if e == nil || s.Approved {
					t.Fatal("invalid attention response accepted")
				}
			} else if e != nil || s.Approved != tc.approved || s.Continuation != "unavailable" {
				t.Fatalf("%+v %v", s, e)
			}
			f.result.Item.RequestSnapshot = []byte(`{}`)
			if _, e = r.Status(context.Background(), a.OperationID, 0); e == nil {
				t.Fatal("forged snapshot accepted")
			}
		})
	}
}
func TestCorrelationLimitsAndRestart(t *testing.T) {
	f := &fakeClient{}
	r, _ := NewRuntime(f, "")
	var alive atomic.Bool
	alive.Store(true)
	a := ask(t, r, `{"a":1}`, alive.Load)
	terminal(f, core.StateResolved, "approval", "approved")
	alive.Store(false)
	if _, e := r.Status(context.Background(), a.OperationID, 0); !errors.Is(e, ErrCorrelation) {
		t.Fatal(e)
	}
	alive.Store(true)
	a = ask(t, r, `{"a":2}`, alive.Load)
	now := time.Now()
	r.now = func() time.Time { return now.Add(PendingTTL + time.Second) }
	if _, e := r.Status(context.Background(), a.OperationID, 0); !errors.Is(e, ErrCorrelation) {
		t.Fatal(e)
	}
	r.now = time.Now
	for i := 0; i < PendingCapacity; i++ {
		if a = ask(t, r, fmt.Sprintf(`{"a":%d}`, i), alive.Load); a.Unavailable != "" {
			t.Fatal(a)
		}
	}
	if a = ask(t, r, `{}`, alive.Load); a.Unavailable != "capacity" {
		t.Fatal(a)
	}
	restarted, _ := NewRuntime(f, "")
	if _, e := restarted.Status(context.Background(), a.OperationID, 0); !errors.Is(e, ErrCorrelation) {
		t.Fatal(e)
	}
}
func TestDistinctBindingsAndConcurrentStatus(t *testing.T) {
	f := &fakeClient{}
	r, _ := NewRuntime(f, "")
	a := ask(t, r, `{"a":1}`, func() bool { return true })
	ask(t, r, `{"a":2}`, func() bool { return true })
	ask(t, r, `{"a":1}`, func() bool { return true })
	if f.requests[0].IdempotencyKey == f.requests[1].IdempotencyKey || f.requests[0].IdempotencyKey == f.requests[2].IdempotencyKey || bindingID(f.requests[0], 4) == bindingID(f.requests[1], 4) {
		t.Fatal("bindings collided")
	}
	terminal(f, core.StateResolved, "approval", "approved")
	var wg sync.WaitGroup
	for i := 0; i < 30; i++ {
		wg.Go(func() {
			s, e := r.Status(context.Background(), a.OperationID, 0)
			if !errors.Is(e, ErrStatusBusy) && (e != nil || !s.Approved) {
				t.Errorf("%+v %v", s, e)
			}
		})
	}
	wg.Wait()
	if len(f.requests) != 3 {
		t.Fatal("status enqueued/replayed")
	}
}
func TestUnavailableAndInvalidAsk(t *testing.T) {
	f := &fakeClient{err: errors.New("lost response")}
	r, _ := NewRuntime(f, "")
	a := ask(t, r, `{}`, func() bool { return true })
	if a.Unavailable != "enqueue_unavailable" || a.ItemID != "" {
		t.Fatal(a)
	}
	for _, raw := range []string{`{"status":"ask"}`, `{"status":"ask","ask":{"prompt":"p","options":"bad","state":"approved","item_id":"forged"}}`, `{"status":"ask","ask":{"prompt":" "}}`, `{"status":"ask","ask":{"prompt":"p","kind":"other"}}`, `{"status":"ask","data":{},"ask":{"prompt":"p"}}`, `{"status":"ask","ask":{"prompt":"p","expires_at":"yesterday"}}`} {
		before := len(f.requests)
		out := r.Attach(context.Background(), "verb", nil, "gen", func() bool { return true }, []byte(raw))
		var env contract.ResultEnvelope
		_ = json.Unmarshal(out, &env)
		if env.Ask == nil || env.Ask.State != "unavailable" || env.Ask.ItemID != "" {
			t.Fatal(string(out))
		}
		if len(f.requests) != before {
			t.Fatal(string(out))
		}
	}
}
func TestLinksAndCanonical(t *testing.T) {
	r, _ := NewRuntime(nil, "http://127.0.0.1:1234")
	for _, bad := range []string{"https://evil/hitl/items/1", "//evil/hitl/items/1", "javascript:alert(1)", "/other", "/hitl/items/1?token=bad"} {
		if r.itemLink(bad, "1") != "" {
			t.Fatal(bad)
		}
	}
	if r.itemLink("/hitl/items/1", "1") == "" {
		t.Fatal("safe link rejected")
	}
	if _, e := Canonical([]byte(`{} {}`)); e == nil {
		t.Fatal("trailing JSON")
	}
	a, _ := Canonical([]byte(`{"b":2,"a":9007199254740993}`))
	b, _ := Canonical([]byte(`{"a":9007199254740993,"b":2}`))
	if string(a) != string(b) {
		t.Fatal("canonical mismatch")
	}
}

func TestForgedTerminalIdentityAndAttention(t *testing.T) {
	f := &fakeClient{}
	r, _ := NewRuntime(f, "")
	raw := []byte(`{"status":"ask","ask":{"prompt":"Look","kind":"attention"}}`)
	out := r.Attach(context.Background(), "work_write", nil, "gen", func() bool { return true }, raw)
	var env contract.ResultEnvelope
	_ = json.Unmarshal(out, &env)
	terminal(f, core.StateResolved, "approval", "approved")
	s, e := r.Status(context.Background(), env.Ask.OperationID, 0)
	if e == nil || s.Approved {
		t.Fatal(s, e)
	}
	f.result.Item.TerminalOutcome = core.Resolved{ItemID: "other", InteractionRevision: 2, Resolution: core.ResolutionRecord{ResolutionID: "res", Response: core.Response{Kind: "approval", Decision: "approved"}, Participant: core.Participant{PrincipalRef: "op", Assurance: "local"}, ResolvedAt: time.Now(), InteractionRevision: 2}}
	if _, e = r.Status(context.Background(), env.Ask.OperationID, 0); e == nil {
		t.Fatal("wrong outcome identity")
	}
	terminal(f, core.StateResolved, "approval", "approved")
	f.result.Item.State = core.StatePresented
	if _, e = r.Status(context.Background(), env.Ask.OperationID, 0); e == nil {
		t.Fatal("nonterminal approval")
	}
}

type waitingClient struct {
	*fakeClient
	started, release chan struct{}
}

func (f *waitingClient) Retrieve(ctx context.Context, id string, wait int) (core.RetrievalResult, error) {
	close(f.started)
	select {
	case <-f.release:
	case <-ctx.Done():
		return core.RetrievalResult{}, ctx.Err()
	}
	r, e := f.fakeClient.Retrieve(ctx, id, wait)
	r.Mode = core.ModeAwait
	r.WaitStatus = core.WaitTerminal
	return r, e
}
func TestRestartWhileAwaitingApprovalFailsClosed(t *testing.T) {
	f := &waitingClient{fakeClient: &fakeClient{}, started: make(chan struct{}), release: make(chan struct{})}
	r, _ := NewRuntime(f, "")
	var alive atomic.Bool
	alive.Store(true)
	a := ask(t, r, `{}`, alive.Load)
	terminal(f.fakeClient, core.StateResolved, "approval", "approved")
	done := make(chan error, 1)
	go func() {
		s, e := r.Status(context.Background(), a.OperationID, 50)
		if s.Approved {
			t.Error("approval survived process replacement")
		}
		done <- e
	}()
	<-f.started
	alive.Store(false)
	close(f.release)
	if e := <-done; !errors.Is(e, ErrCorrelation) {
		t.Fatal(e)
	}
	if len(f.requests) != 1 {
		t.Fatal("await replayed provider/enqueue")
	}
}

func bindingID(req EnqueueRequest, index int) any {
	return req.Correlations["additional"].([]any)[index].(map[string]any)["id"]
}

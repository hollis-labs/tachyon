package cardevents

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/libs/harness/core/adapters/runtimeevents/ingest"
	"github.com/hollis-labs/libs/mesh/core/hitl"
	"github.com/hollis-labs/tachyon/internal/approvals"
	"github.com/hollis-labs/tachyon/internal/cardfx"
	"github.com/hollis-labs/tachyon/internal/localstore"
)

type rig struct {
	inbox *approvals.Inbox
	fx    *cardfx.Store
	out   *Store
	col   *Collector
}

func newRig(t *testing.T) rig {
	t.Helper()
	db, err := localstore.Open(context.Background(), filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	items := &approvals.Store{DB: db}
	r := rig{inbox: approvals.NewInbox(items, nil), fx: &cardfx.Store{DB: db}, out: &Store{DB: db}}
	r.col = &Collector{Items: items, Effects: r.fx, Outbox: r.out}
	return r
}

func (r rig) ask(t *testing.T, key string, kind string, extra map[string]any) string {
	t.Helper()
	req := hitl.EnqueueRequest{ContractVersion: hitl.ContractVersion, Kind: kind, IdempotencyKey: key, Source: hitl.SourceAssertion{AgentID: "planner-1"}, Extra: map[string]json.RawMessage{}}
	base := map[string]any{"title": "SECRET-TITLE", "data": map[string]any{"x": "SECRET-DATA"}}
	for k, v := range extra {
		base[k] = v
	}
	for k, v := range base {
		req.Extra[k], _ = json.Marshal(v)
	}
	if err := approvals.CardRegistry.Prepare(&req, "service:abc"); err != nil {
		t.Fatal(err)
	}
	h, err := r.inbox.Service.Enqueue(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	return h.ItemID
}

func (r rig) approve(t *testing.T, id string) {
	t.Helper()
	rev := int64(1)
	who := hitl.Participant{PrincipalRef: "browser-session:SECRET-WHO", Authority: "a", Assurance: "operator-token", Responder: &hitl.Responder{Kind: "human-session", Ref: "browser-session:SECRET-WHO"}}
	if _, err := r.inbox.Service.Respond(context.Background(), hitl.RespondCommand{ItemID: id, ExpectedRevision: &rev, Response: hitl.Response{Kind: "card.approval", Decision: "approved", Note: "SECRET-NOTE"}, Participant: who}); err != nil {
		t.Fatal(err)
	}
}

func (r rig) rows(t *testing.T) []Row {
	t.Helper()
	rows, err := r.out.Pending(context.Background(), 100)
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func kinds(rows []Row) string {
	var k []string
	for _, r := range rows {
		k = append(k, r.Kind)
	}
	return strings.Join(k, ",")
}

func TestLifecycleQueuesValidIdAndCodeOnlyEvents(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	id := r.ask(t, "k1", "card.approval", map[string]any{"effects": []map[string]any{{"target": "torque:task/CW-20261023-0001", "op": "status", "value": "done"}}})
	if err := r.col.Pass(ctx); err != nil {
		t.Fatal(err)
	}
	if got := kinds(r.rows(t)); got != "card.created" {
		t.Fatalf("a pending card queues only created: %s", got)
	}
	r.approve(t, id)
	if err := r.col.Pass(ctx); err != nil {
		t.Fatal(err)
	}
	if got := kinds(r.rows(t)); got != "card.created,card.resolved" {
		t.Fatalf("approved, receipt not yet final: %s", got)
	}
	// The receipt becoming final completes the set.
	if won, err := r.fx.Claim(ctx, id, time.Now()); err != nil || !won {
		t.Fatal(won, err)
	}
	if err := r.fx.Finish(ctx, cardfx.Receipt{ItemID: id, State: cardfx.StateCompleted, Effects: []cardfx.EffectResult{{Index: 0, Target: "SECRET-TARGET", Status: "ok", Detail: "SECRET-DETAIL"}}}, time.Now()); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := r.col.Pass(ctx); err != nil {
			t.Fatal(err)
		}
	}
	rows := r.rows(t)
	if got := kinds(rows); got != "card.created,card.resolved,card.effect.completed" {
		t.Fatalf("%s", got)
	}
	if left, _ := r.out.Unscanned(ctx, 10); len(left) != 0 {
		t.Fatalf("the item should be done: %v", left)
	}
	for i, row := range rows {
		if _, rej := ingest.CheckItem("tachyon", row.Event, ingest.Options{}); rej != nil {
			t.Fatalf("%s would be rejected by the hub: %v", row.Kind, rej)
		}
		for _, secret := range []string{"SECRET-TITLE", "SECRET-DATA", "SECRET-WHO", "SECRET-NOTE", "SECRET-TARGET", "SECRET-DETAIL"} {
			if strings.Contains(string(row.Event), secret) {
				t.Fatalf("%s leaks %s: %s", row.Kind, secret, row.Event)
			}
		}
		if row.Seq != int64(i+1) {
			t.Fatalf("sequence must be monotonic: %d", row.Seq)
		}
	}
	// Re-running queues nothing new and event ids stay stable.
	again := r.rows(t)
	if err := r.col.Pass(ctx); err != nil || len(r.rows(t)) != len(again) || again[0].EventID != rows[0].EventID {
		t.Fatalf("collector must be idempotent")
	}
}

func TestExpiredCanceledAndOlderKindsAreHandled(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	id := r.ask(t, "k2", "card.question", map[string]any{"data": map[string]any{"question": "?"}})
	if _, err := r.inbox.Service.Withdraw(ctx, hitl.WithdrawCommand{ContractVersion: hitl.ContractVersion, ItemID: id, Caller: hitl.CallerAssertion{ApplicationID: "service:abc"}}); err != nil {
		t.Fatal(err)
	}
	// An older approval-kind item is not a card: no events.
	old := hitl.EnqueueRequest{ContractVersion: hitl.ContractVersion, Kind: "approval", IdempotencyKey: "old", Source: hitl.SourceAssertion{ApplicationID: "x", AgentID: "a"}, Extra: map[string]json.RawMessage{}}
	_, _ = r.inbox.Service.Enqueue(ctx, old) // may be refused by validation; irrelevant
	if err := r.col.Pass(ctx); err != nil {
		t.Fatal(err)
	}
	rows := r.rows(t)
	if got := kinds(rows); got != "card.created,card.updated" {
		t.Fatalf("%s", got)
	}
	var ev struct{ Payload map[string]any }
	_ = json.Unmarshal(rows[1].Event, &ev)
	if ev.Payload["state"] != "canceled" {
		t.Fatalf("%v", ev.Payload)
	}
}

func TestSenderMarksSentRetriesOutagesAndNeverResendsRejections(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	r.ask(t, "k3", "card.notice", nil)
	r.ask(t, "k4", "card.notice", nil)
	if err := r.col.Pass(ctx); err != nil {
		t.Fatal(err)
	}
	mode := "down"
	var auth string
	var seen [][]string
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		auth = req.Header.Get("Authorization")
		var body struct{ Events []struct{ ID string } }
		_ = json.NewDecoder(req.Body).Decode(&body)
		var ids []string
		var results []ingest.Result
		for i, e := range body.Events {
			ids = append(ids, e.ID)
			res := ingest.Result{ID: e.ID, Status: ingest.StatusAccepted, Cursor: "c"}
			if mode == "reject-first" && i == 0 {
				res = ingest.Result{ID: e.ID, Status: ingest.StatusRejected, Code: ingest.CodeForbiddenKind}
			}
			results = append(results, res)
		}
		seen = append(seen, ids)
		if mode == "down" {
			w.WriteHeader(503)
			return
		}
		_ = json.NewEncoder(w).Encode(ingest.NewResponse(results))
	}))
	defer hub.Close()
	tokenFile := filepath.Join(t.TempDir(), "tok")
	if err := os.WriteFile(tokenFile, []byte("hub-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := NewSender(r.out, hub.URL, tokenFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Flush(ctx); err == nil || len(r.rows(t)) != 2 {
		t.Fatalf("an outage must leave events pending: %v", err)
	}
	mode = "reject-first"
	if err := s.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	if left := r.rows(t); len(left) != 0 {
		t.Fatalf("everything was answered: %v", kinds(left))
	}
	if auth != "Bearer hub-token" {
		t.Fatalf("credential comes from the token file: %q", auth)
	}
	if len(seen) != 2 || strings.Join(seen[0], ",") != strings.Join(seen[1], ",") {
		t.Fatalf("a retry must resend the same event ids: %v", seen)
	}
	if err := s.Flush(ctx); err != nil || len(seen) != 2 {
		t.Fatalf("nothing pending, nothing sent: %v %v", err, seen)
	}
}

func TestSenderNeedsAnExplicitCredentialFile(t *testing.T) {
	if _, err := NewSender(&Store{}, "http://127.0.0.1:1", ""); err == nil {
		t.Fatal("no token file must mean no emitter")
	}
	if _, err := NewSender(&Store{}, "", "/tmp/x"); err == nil {
		t.Fatal("no hub URL must mean no emitter")
	}
}

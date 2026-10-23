package carddelivery

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/libs/mesh/core/hitl"
	"github.com/hollis-labs/libs/mesh/core/messaging"
	tether "github.com/hollis-labs/libs/mesh/core/tetherclient"
	"github.com/hollis-labs/tachyon/internal/approvals"
	"github.com/hollis-labs/tachyon/internal/localstore"
)

type fakeSink struct {
	got  []Message
	errs []error
}

func (f *fakeSink) Deliver(_ context.Context, m Message) (string, error) {
	f.got = append(f.got, m)
	if len(f.errs) > 0 {
		e := f.errs[0]
		f.errs = f.errs[1:]
		if e != nil {
			return "", e
		}
	}
	return "rcpt-" + m.Key, nil
}

const profileJSON = `{"version":"1","rules":[
 {"id":"approvals","match":{"kind":"card.approval"},"sink":"ops","completion":"wait","delivery":"durable_message"},
 {"id":"notices","match":{"kind":"card.notice"},"sink":"tachyon","completion":"async","delivery":"await"}],
 "catch_all":{"sink":"tachyon","completion":"async","delivery":"await"}}`

type env struct {
	inbox *approvals.Inbox
	d     *Dispatcher
	ops   *fakeSink
	now   time.Time
}

func setup(t *testing.T) *env {
	t.Helper()
	db, err := localstore.Open(context.Background(), filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	dir := t.TempDir()
	path := filepath.Join(dir, "d.json")
	writeFile(t, path, `{"from":"msg://service/tachyon/cards","sinks":{"ops":{"kind":"tether_message","to":"msg://agent/agent-mux/asker"}},"profile":`+profileJSON+`}`)
	router, _, err := Load(path, nopNotifier{})
	if err != nil {
		t.Fatal(err)
	}
	items := &approvals.Store{DB: db}
	e := &env{inbox: approvals.NewInbox(items, nil), ops: &fakeSink{}, now: time.Now().UTC()}
	e.d = New(items, &Store{DB: db}, router, HoldSink, map[string]Sink{"ops": e.ops})
	e.d.Now = func() time.Time { return e.now }
	return e
}

type nopNotifier struct{}

func (nopNotifier) Notify(context.Context, tether.NotifyRequest) (tether.NotifyResult, error) {
	return tether.NotifyResult{}, nil
}

func ask(t *testing.T, e *env, kind, key string) string {
	t.Helper()
	req := hitl.EnqueueRequest{ContractVersion: hitl.ContractVersion, Kind: kind, IdempotencyKey: key, Source: hitl.SourceAssertion{AgentID: "planner-1"}, Extra: map[string]json.RawMessage{}}
	for k, v := range map[string]any{"title": "Ask", "data": map[string]any{"n": 1}} {
		req.Extra[k], _ = json.Marshal(v)
	}
	if err := approvals.CardRegistry.Prepare(&req, "service:planner"); err != nil {
		t.Fatal(err)
	}
	h, err := e.inbox.Service.Enqueue(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	return h.ItemID
}

func answer(t *testing.T, e *env, id, kind, decision string) {
	t.Helper()
	rev := int64(1)
	who := hitl.Participant{PrincipalRef: "browser-session:op", Authority: "tachyon-inbox", Assurance: "operator-token", Responder: &hitl.Responder{Kind: "human-session", Ref: "browser-session:op"}}
	if _, err := e.inbox.Service.Respond(context.Background(), hitl.RespondCommand{ItemID: id, ExpectedRevision: &rev, Response: hitl.Response{Kind: kind, Decision: decision, Note: "ok"}, Participant: who}); err != nil {
		t.Fatal(err)
	}
}

func TestResolvedCardIsPushedOnceToItsRoutedSink(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	id := ask(t, e, "card.approval", "k1")
	if err := e.d.Run(ctx); err != nil || len(e.ops.got) != 0 {
		t.Fatalf("a pending card must not be delivered: %v %v", err, e.ops.got)
	}
	answer(t, e, id, "card.approval", "approved")
	for i := 0; i < 3; i++ {
		if err := e.d.Run(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if len(e.ops.got) != 1 {
		t.Fatalf("exactly one push expected, got %d", len(e.ops.got))
	}
	m := e.ops.got[0]
	if m.ItemID != id || m.CardRef != "tach:card/"+id || m.Terminal != "resolved" || m.Route.RuleID != "approvals" || !strings.Contains(string(m.Envelope), `"approved"`) {
		t.Fatalf("%+v %s", m, m.Envelope)
	}
	row, _ := e.d.Store.Get(ctx, id)
	if row == nil || row.State != StateSent || row.Receipt == "" {
		t.Fatalf("%+v", row)
	}
}

func TestAwaitRoutesAreHeldNotPushed(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	id := ask(t, e, "card.notice", "k2")
	answer(t, e, id, "card.notice", "acknowledged")
	if err := e.d.Run(ctx); err != nil || len(e.ops.got) != 0 {
		t.Fatalf("%v %v", err, e.ops.got)
	}
	row, _ := e.d.Store.Get(ctx, id)
	if row == nil || row.State != StateHeld || row.Delivery != "await" || row.RuleID != "notices" {
		t.Fatalf("%+v", row)
	}
}

func TestTransientFailureRetriesWithTheSameKeyThenSucceeds(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	e.ops.errs = []error{errors.New("tether down"), nil}
	id := ask(t, e, "card.approval", "k3")
	answer(t, e, id, "card.approval", "denied")
	if err := e.d.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if row, _ := e.d.Store.Get(ctx, id); row.State != StatePending || row.Attempts != 1 {
		t.Fatalf("%+v", row)
	}
	if err := e.d.Run(ctx); err != nil || len(e.ops.got) != 1 {
		t.Fatalf("backoff must hold the retry back: %v %d", err, len(e.ops.got))
	}
	e.now = e.now.Add(time.Hour)
	if err := e.d.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if len(e.ops.got) != 2 || e.ops.got[0].Key != e.ops.got[1].Key {
		t.Fatalf("the retry must reuse the key: %+v", e.ops.got)
	}
	if row, _ := e.d.Store.Get(ctx, id); row.State != StateSent {
		t.Fatalf("%+v", row)
	}
}

func TestPermanentRefusalAndUnknownSinkEndAsFailed(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	e.ops.errs = []error{ErrPermanent}
	id := ask(t, e, "card.approval", "k4")
	answer(t, e, id, "card.approval", "approved")
	if err := e.d.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if row, _ := e.d.Store.Get(ctx, id); row.State != StateFailed {
		t.Fatalf("%+v", row)
	}
	e.d.Sinks = map[string]Sink{}
	id2 := ask(t, e, "card.approval", "k5")
	answer(t, e, id2, "card.approval", "approved")
	if err := e.d.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if row, _ := e.d.Store.Get(ctx, id2); row.State != StateFailed || !strings.Contains(row.Error, "not configured") {
		t.Fatalf("%+v", row)
	}
}

func TestLoadRefusesAProfileNamingAnUndefinedSink(t *testing.T) {
	path := filepath.Join(t.TempDir(), "d.json")
	writeFile(t, path, `{"from":"msg://service/tachyon/cards","sinks":{},"profile":`+profileJSON+`}`)
	if _, _, err := Load(path, nopNotifier{}); err == nil || !strings.Contains(err.Error(), `"ops"`) {
		t.Fatalf("%v", err)
	}
}

type recNotifier struct {
	req tether.NotifyRequest
	err error
}

func (r *recNotifier) Notify(_ context.Context, req tether.NotifyRequest) (tether.NotifyResult, error) {
	r.req = req
	return tether.NotifyResult{}, r.err
}

func TestTetherSinkWakesUnlessNotifyAndClassifiesRefusals(t *testing.T) {
	to := mustAddr(t, "msg://agent/agent-mux/asker")
	rn := &recNotifier{}
	s := TetherSink{N: rn, From: mustAddr(t, "msg://service/tachyon/cards"), To: to}
	m := Message{Key: "k", ItemID: "i", CardRef: "tach:card/i", Envelope: json.RawMessage(`{"status":"submitted"}`)}
	m.Route.Completion = "wait"
	if _, err := s.Deliver(context.Background(), m); err != nil || rn.req.Wake == nil || !*rn.req.Wake || rn.req.Metadata["card_delivery_key"] != "k" || rn.req.To != to {
		t.Fatalf("%v %+v", err, rn.req)
	}
	m.Route.Completion = "notify"
	_, _ = s.Deliver(context.Background(), m)
	if *rn.req.Wake {
		t.Fatal("notify completion must not wake")
	}
	rn.err = &tether.APIError{StatusCode: http.StatusForbidden}
	if _, err := s.Deliver(context.Background(), m); !errors.Is(err, ErrPermanent) {
		t.Fatalf("a 403 is permanent: %v", err)
	}
	rn.err = &tether.APIError{StatusCode: http.StatusBadGateway}
	if _, err := s.Deliver(context.Background(), m); err == nil || errors.Is(err, ErrPermanent) {
		t.Fatalf("a 502 is retried: %v", err)
	}
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func mustAddr(t *testing.T, s string) messaging.Address {
	t.Helper()
	var a messaging.Address
	if err := json.Unmarshal([]byte(`"`+s+`"`), &a); err != nil {
		t.Fatal(err)
	}
	return a
}

// A fake Tether hub that, like the real one, stores a send once per
// metadata["tether.message_id"] and can lose the response after committing.
type hub struct {
	stored   map[string]string // idempotency key -> message id
	bodies   int
	dropNext bool
}

func (h *hub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var req tether.NotifyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	key := req.Metadata["tether.message_id"]
	id, dup := h.stored[key]
	if !dup || key == "" {
		h.bodies++
		id = "msg-" + strings.Repeat("x", h.bodies)
		if key != "" {
			h.stored[key] = id
		}
	}
	if h.dropNext {
		h.dropNext = false
		conn, _, _ := w.(http.Hijacker).Hijack()
		conn.Close()
		return
	}
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]any{"message": map[string]any{"id": id}})
}

func TestLostResponseRetryAcrossRestartStoresOneTetherMessage(t *testing.T) {
	h := &hub{stored: map[string]string{}, dropNext: true}
	srv := httptest.NewServer(h)
	defer srv.Close()
	client, err := tether.New(srv.URL, tether.WithToken(""))
	if err != nil {
		t.Fatal(err)
	}
	e := setup(t)
	path := filepath.Join(t.TempDir(), "d.json")
	writeFile(t, path, `{"from":"msg://service/tachyon/cards","sinks":{"ops":{"kind":"tether_message","to":"msg://agent/agent-mux/asker"}},"profile":`+profileJSON+`}`)
	build := func() *Dispatcher {
		router, sinks, err := Load(path, client)
		if err != nil {
			t.Fatal(err)
		}
		d := New(e.d.Items, e.d.Store, router, HoldSink, sinks)
		d.Now = func() time.Time { return e.now }
		return d
	}
	ctx := context.Background()
	id := ask(t, e, "card.approval", "k-lost")
	answer(t, e, id, "card.approval", "approved")
	if err := build().Run(ctx); err != nil {
		t.Fatal(err)
	}
	if row, _ := e.d.Store.Get(ctx, id); row.State != StatePending || len(h.stored) != 1 {
		t.Fatalf("the first send committed but its response was lost: %+v stored=%d", row, len(h.stored))
	}
	// Restart: a new dispatcher over the same delivery row, clock advanced.
	e.now = e.now.Add(time.Hour)
	if err := build().Run(ctx); err != nil {
		t.Fatal(err)
	}
	row, _ := e.d.Store.Get(ctx, id)
	if row.State != StateSent || h.bodies != 1 || len(h.stored) != 1 || row.Receipt != h.stored[row.Key] {
		t.Fatalf("the retry must reuse the stored Tether message: %+v bodies=%d stored=%v", row, h.bodies, h.stored)
	}
}

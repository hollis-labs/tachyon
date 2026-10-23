package approvals

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hollis-labs/libs/mesh/core/hitl"
)

func cardRequest(key string, extra map[string]any) hitl.EnqueueRequest {
	r := hitl.EnqueueRequest{
		ContractVersion: hitl.ContractVersion, Kind: "card.approval", IdempotencyKey: key,
		Source: hitl.SourceAssertion{ApplicationID: "claimed-by-asker", AgentID: "planner-1"},
		Extra:  map[string]json.RawMessage{},
	}
	base := map[string]any{"title": "Apply 2 status changes", "data": map[string]any{"changes": 2}}
	for k, v := range extra {
		base[k] = v
	}
	for k, v := range base {
		r.Extra[k], _ = json.Marshal(v)
	}
	return r
}

func TestCardRequestsUseServerOwnedClassAndSource(t *testing.T) {
	policy := ProducerPolicy{ApplicationID: "planner", MinimumClass: "medium"}
	req := cardRequest("k1", map[string]any{"class": "low"})
	if err := prepareRequest(&req, policy); err != nil {
		t.Fatal(err)
	}
	if req.Source.ApplicationID != "planner" {
		t.Fatalf("source must come from the producer policy, got %q", req.Source.ApplicationID)
	}
	if got := extraString(req, "class"); got != "medium" {
		t.Fatalf("producer floor must raise the class, got %q", got)
	}
	if extraString(req, "op_digest") == "" {
		t.Fatal("digest must be computed by the server")
	}
	// Cerberus is provider-handoff only, so a card from it is high and needs a handoff URL.
	cerb := cardRequest("k2", nil)
	err := prepareRequest(&cerb, ProducerPolicy{ApplicationID: "cerberus", MinimumClass: "high", HandoffOrigins: []string{"https://cerberus.example"}})
	if !errors.Is(err, hitl.ErrInvalidRequest) {
		t.Fatalf("a high card without a handoff URL must be refused: %v", err)
	}
	// The older kinds still go through their own validation.
	old := requestFixture(t, "old", "low")
	if err := prepareRequest(&old, ProducerPolicy{ApplicationID: "test-producer", MinimumClass: "low", HandoffOrigins: []string{"https://cerberus.example"}}); err != nil {
		t.Fatalf("approval requests must be unaffected: %v", err)
	}
}

func TestCardRoundTripRecordsResponder(t *testing.T) {
	inbox := NewInbox(nativeStore(t), nil)
	ctx := context.Background()
	req := cardRequest("k3", nil)
	if err := prepareRequest(&req, ProducerPolicy{ApplicationID: "planner", MinimumClass: "low"}); err != nil {
		t.Fatal(err)
	}
	handle, err := inbox.Service.Enqueue(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	who := hitl.Participant{
		PrincipalRef: "browser-session:abc", Authority: "tachyon-inbox/tangent-session", Assurance: "operator-token",
		Responder: &hitl.Responder{Kind: "human-session", Ref: "browser-session:abc"},
	}
	rev := int64(1)
	bad := hitl.Response{Kind: "card.approval", Decision: "acknowledged"}
	if _, err := inbox.Service.Respond(ctx, hitl.RespondCommand{ItemID: handle.ItemID, ExpectedRevision: &rev, Response: bad, Participant: who}); !errors.Is(err, hitl.ErrInvalidRequest) {
		t.Fatalf("a decision outside the kind must be refused: %v", err)
	}
	ok := hitl.Response{Kind: "card.approval", Decision: "approved", Extra: map[string]json.RawMessage{"note": json.RawMessage(`"go"`)}}
	if _, err := inbox.Service.Respond(ctx, hitl.RespondCommand{ItemID: handle.ItemID, ExpectedRevision: &rev, Response: ok, Participant: who}); err != nil {
		t.Fatal(err)
	}
	rec, err := inbox.Store.Get(ctx, handle.ItemID)
	if err != nil || rec.State != hitl.StateResolved {
		t.Fatalf("%+v %v", rec, err)
	}
	raw, _ := json.Marshal(rec)
	for _, want := range []string{`"assurance":"operator-token"`, `browser-session:abc`} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("resolution must retain %s: %s", want, raw)
		}
	}
}

// Card bodies reach the browser only through the session-gated inbox routes;
// the unauthenticated events poll route carries ids and codes, never a body.
func TestCardBodiesNeedAnOperatorBrowserSession(t *testing.T) {
	sessions := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeValue(w, map[string]any{"elevated": false, "assurance": "browser-session"})
	}))
	defer sessions.Close()
	inbox := NewInbox(nativeStore(t), New(sessions.URL))
	req := cardRequest("k-body", map[string]any{"data": map[string]any{"secretish": "proposed change set"}})
	if err := prepareRequest(&req, ProducerPolicy{ApplicationID: "planner", MinimumClass: "low"}); err != nil {
		t.Fatal(err)
	}
	handle, err := inbox.Service.Enqueue(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{Prefix + "/inbox", Prefix + "/items/" + handle.ItemID} {
		if w := serveJSON(inbox, "GET", path, nil, "", ""); w.Code != 401 || strings.Contains(w.Body.String(), "proposed change set") {
			t.Fatalf("%s without a session: %d %s", path, w.Code, w.Body)
		}
		if w := serveJSON(inbox, "GET", path, nil, "producer-token", "session"); w.Code != 403 || strings.Contains(w.Body.String(), "proposed change set") {
			t.Fatalf("%s with a bearer token: %d %s", path, w.Code, w.Body)
		}
		if w := serveJSON(inbox, "GET", path, nil, "", "session"); w.Code != 200 || !strings.Contains(w.Body.String(), "proposed change set") {
			t.Fatalf("%s with a browser session: %d %s", path, w.Code, w.Body)
		}
	}
}

package dataverbs

import (
	"encoding/json"
	"io"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/libs/mesh/core/hitl"
	"github.com/hollis-labs/tachyon/internal/agentaccess"
	"github.com/hollis-labs/tachyon/internal/approvals"
)

func TestAgentCardToolsAskAwaitAndStayScoped(t *testing.T) {
	_, s, db := setup(t)
	access := &agentaccess.Store{DB: db}
	items := &approvals.Store{DB: db}
	inbox := approvals.NewInbox(items, nil)

	a, secretA, err := access.Issue(ctx, "", "Agent A", agentaccess.CardAsk(), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	_, secretB, err := access.Issue(ctx, "", "Agent B", agentaccess.CardAsk(), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	_, reader, err := access.Issue(ctx, "", "Reader", agentaccess.PortfolioRead(), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	h := NewAgentHTTP(s, access, &Cards{Service: inbox.Service, Registry: approvals.CardRegistry})
	call := func(method, verb, input, credential string, status int) map[string]any {
		t.Helper()
		path := AgentPrefix + "/providers/tachyon/verbs/" + verb
		var body io.Reader
		if method == "GET" {
			path += "?input=" + url.QueryEscape(input)
		} else {
			body = strings.NewReader(input)
		}
		req := httptest.NewRequest(method, path, body)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+credential)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != status {
			t.Fatalf("%s: got %d want %d: %s", verb, w.Code, status, w.Body.String())
		}
		var out map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		return out
	}

	// A credential without the card grant does not see the tools.
	call("POST", "card.ask", `{"kind":"card.notice","title":"x"}`, reader, 404)
	// Unknown kinds, a lowered or invalid class, and a client digest are refused.
	call("POST", "card.ask", `{"kind":"card.bogus","title":"x"}`, secretA, 400)
	call("POST", "card.ask", `{"kind":"card.approval","title":"x","class":"urgent"}`, secretA, 400)
	call("POST", "card.ask", `{"kind":"card.approval","title":"x","op_digest":"sha256:00"}`, secretA, 400)
	call("POST", "card.ask", `{"kind":"card.approval","title":"x","ttl_seconds":5}`, secretA, 400)

	ask := call("POST", "card.ask", `{"kind":"card.approval","title":"Apply 2 status changes","data":{"changes":2},"completion":"wait","effects":[{"target":"torque:task/1"}],"key":"k1"}`, secretA, 200)["data"].(map[string]any)
	id := ask["id"].(string)
	if ask["link"] != "tach:card/"+id || ask["class"] != "medium" || ask["state"] != "presented" || !strings.HasPrefix(ask["op_digest"].(string), "sha256:") || ask["expires_at"] == nil {
		t.Fatalf("%v", ask)
	}
	// The same key returns the same card, not a duplicate.
	again := call("POST", "card.ask", `{"kind":"card.approval","title":"Apply 2 status changes","data":{"changes":2},"completion":"wait","effects":[{"target":"torque:task/1"}],"key":"k1"}`, secretA, 200)["data"].(map[string]any)
	if again["id"] != id {
		t.Fatalf("idempotent ask made a second card: %v vs %v", again["id"], id)
	}
	// Reusing the key for a different card is a conflict.
	call("POST", "card.ask", `{"kind":"card.approval","title":"Something else","key":"k1"}`, secretA, 409)
	// The queue holds the verified application, never what the agent might claim.
	rec, err := items.Get(ctx, id)
	if err != nil || rec.CallerScope != "service:"+a.IdentityID {
		t.Fatalf("%+v %v", rec, err)
	}

	// Another credential cannot read, await or cancel it.
	call("GET", "card.get", `{"id":"`+id+`"}`, secretB, 404)
	call("GET", "card.await", `{"id":"`+id+`","wait_ms":0}`, secretB, 404)
	call("POST", "card.cancel", `{"id":"`+id+`"}`, secretB, 404)

	pending := call("GET", "card.await", `{"id":"`+id+`","wait_ms":0}`, secretA, 200)["data"].(map[string]any)
	if pending["finished"] != false || pending["wait_status"] != "timeout" || pending["response"] != nil {
		t.Fatalf("%v", pending)
	}
	call("GET", "card.await", `{"id":"`+id+`","wait_ms":60000}`, secretA, 400)

	rev := rec.Revision
	if _, err := inbox.Service.Respond(ctx, hitl.RespondCommand{ItemID: id, ExpectedRevision: &rev,
		Response:    hitl.Response{Kind: "card.approval", Decision: "approved", Extra: map[string]json.RawMessage{"note": json.RawMessage(`"go"`)}},
		Participant: hitl.Participant{PrincipalRef: "browser-session:t", Assurance: "operator-token", Responder: &hitl.Responder{Kind: "human-session", Ref: "browser-session:t"}}}); err != nil {
		t.Fatal(err)
	}
	done := call("GET", "card.await", `{"id":"`+id+`","wait_ms":2000}`, secretA, 200)["data"].(map[string]any)
	resp, _ := done["response"].(map[string]any)
	decisions, _ := resp["decisions"].([]any)
	if done["finished"] != true || done["state"] != "resolved" || resp["status"] != "submitted" || len(decisions) != 1 ||
		decisions[0].(map[string]any)["action"] != "approved" || decisions[0].(map[string]any)["note"] != "go" || done["assurance"] != "operator-token" {
		t.Fatalf("%v", done)
	}

	// A settled card cannot be withdrawn; an open one can.
	call("POST", "card.cancel", `{"id":"`+id+`"}`, secretA, 409)
	open := call("POST", "card.ask", `{"kind":"card.question","title":"Which branch?","completion":"async"}`, secretA, 200)["data"].(map[string]any)
	gone := call("POST", "card.cancel", `{"id":"`+open["id"].(string)+`","reason":"moot"}`, secretA, 200)["data"].(map[string]any)
	if gone["state"] != "canceled" || gone["response"].(map[string]any)["status"] != "canceled" {
		t.Fatalf("%v", gone)
	}
}

func TestCardVerbsAbsentWithoutService(t *testing.T) {
	_, s, db := setup(t)
	access := &agentaccess.Store{DB: db}
	_, secret, err := access.Issue(ctx, "", "Agent", agentaccess.CardAsk(), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	h := NewAgentHTTP(s, access)
	req := httptest.NewRequest("POST", AgentPrefix+"/providers/tachyon/verbs/card.ask", strings.NewReader(`{"kind":"card.notice","title":"x"}`))
	req.Header.Set("Authorization", "Bearer "+secret)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 404 {
		t.Fatalf("cards not wired must not expose the verb: %d %s", w.Code, w.Body)
	}
}

package dataverbs

import (
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/libs/mesh/core/hitl"
	"github.com/hollis-labs/tachyon/internal/actionrun"
	"github.com/hollis-labs/tachyon/internal/agentaccess"
	"github.com/hollis-labs/tachyon/internal/approvals"
)

type oneShot struct{ r *strings.Reader }

func (o oneShot) Read(p []byte) (int, error) { return o.r.Read(p) }
func (oneShot) Write(p []byte) (int, error)  { return len(p), nil }
func (oneShot) Close() error                 { return nil }
func (oneShot) Resize(uint16, uint16) error  { return nil }
func (oneShot) Wait() (int, error)           { return 0, nil }

type fakeMachine struct{ ran chan []string }

func (f fakeMachine) Start(_ context.Context, argv []string, _, _ uint16) (actionrun.Session, error) {
	f.ran <- argv
	return oneShot{strings.NewReader("reloaded\r\n")}, nil
}
func (fakeMachine) Output(context.Context, []string) ([]byte, error) {
	return []byte(`[{"id":"hub-tether","env":"dev","owner":"self","admin":"self"},{"id":"prod-db","env":"prod","owner":"self","admin":"owner"}]`), nil
}

func TestAgentActionToolsRequestAndAwaitOnlyOwnRuns(t *testing.T) {
	_, s, db := setup(t)
	access := &agentaccess.Store{DB: db}
	items := &approvals.Store{DB: db}
	inbox := approvals.NewInbox(items, nil)
	machine := fakeMachine{ran: make(chan []string, 4)}
	mgr := &actionrun.Manager{Store: &actionrun.Store{DB: db}, Items: items, Transports: map[string]actionrun.Transport{"hub": machine, "agent-os": machine}}
	svc := &actionrun.Service{Store: mgr.Store, Manager: mgr, Approval: inbox.Service}

	a, secretA, err := access.Issue(ctx, "", "Agent A", agentaccess.ActionRequest(), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	_, secretB, err := access.Issue(ctx, "", "Agent B", agentaccess.ActionRequest(), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	_, reader, err := access.Issue(ctx, "", "Reader", agentaccess.PortfolioRead(), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	h := NewAgentHTTP(s, access, svc)
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

	cat := call("GET", "actions.catalog", `{}`, secretA, 200)["data"].(map[string]any)
	if len(cat["actions"].([]any)) < 7 || !strings.Contains(cat["rules"].(string), "never ask for a free-form shell") {
		t.Fatalf("%v", cat)
	}
	// A credential without the action verbs does not see them.
	call("GET", "actions.catalog", `{}`, reader, 404)
	call("POST", "actions.request", `{"machine":"hub","action":"cerberus.resource.reload","params":{"resource_id":"hub-tether"},"reason":"x"}`, reader, 404)

	// Validation errors name what is allowed; agents cannot name a command or a digest.
	bad := call("POST", "actions.request", `{"machine":"hub","action":"bash","reason":"x"}`, secretA, 400)
	if !strings.Contains(bad["message"].(string), "cerberus.resource.reload") {
		t.Fatalf("%v", bad)
	}
	call("POST", "actions.request", `{"machine":"hub","action":"cerberus.resource.reload","params":{"resource_id":"hub-tether"},"reason":"x","op_digest":"sha256:00","argv":["sh"]}`, secretA, 400)
	call("POST", "actions.request", `{"machine":"hub","action":"cerberus.resource.stop","params":{"resource_id":"prod-db"},"reason":"x"}`, secretA, 400)

	req := call("POST", "actions.request", `{"machine":"hub","action":"cerberus.resource.reload","params":{"resource_id":"hub-tether"},"reason":"pick up config"}`, secretA, 200)["data"].(map[string]any)
	runID := req["run_id"].(string)
	if req["command"] != "cerberus resource reload hub-tether --ack" || !strings.HasPrefix(req["op_digest"].(string), "sha256:") || req["status"] != "pending_approval" {
		t.Fatalf("%v", req)
	}
	if !strings.Contains(req["approval"].(map[string]any)["link"].(string), "(tach:approval/item_") {
		t.Fatalf("%v", req)
	}
	select {
	case <-machine.ran:
		t.Fatal("ran before approval")
	default:
	}
	// The request's requester is the server-derived identity.
	run, _ := svc.Store.Get(ctx, runID)
	if run.RequestedBy != "service:"+a.IdentityID {
		t.Fatalf("requester %q", run.RequestedBy)
	}
	// Agent B cannot read A's run.
	call("GET", "actions.await", `{"run_id":"`+runID+`","wait_ms":0}`, secretB, 404)
	pending := call("GET", "actions.await", `{"run_id":"`+runID+`","wait_ms":0}`, secretA, 200)["data"].(map[string]any)
	if pending["finished"] != false || pending["status"] != "pending_approval" {
		t.Fatalf("%v", pending)
	}

	rec, _ := items.Get(ctx, run.ItemID)
	rev := rec.Revision
	if _, err := inbox.Service.Respond(ctx, hitl.RespondCommand{ItemID: run.ItemID, ExpectedRevision: &rev,
		Response:    hitl.Response{Kind: "approval", Decision: "approved"},
		Participant: hitl.Participant{PrincipalRef: "t", Assurance: "operator-token", Responder: &hitl.Responder{Kind: "human-session", Ref: "t"}}}); err != nil {
		t.Fatal(err)
	}
	done := call("GET", "actions.await", `{"run_id":"`+runID+`","wait_ms":5000}`, secretA, 200)["data"].(map[string]any)
	if done["finished"] != true || done["status"] != "succeeded" || done["exit_code"] != float64(0) || !strings.Contains(done["output"].(string), "reloaded") {
		t.Fatalf("%v", done)
	}
}

func TestActionVerbsAbsentWithoutService(t *testing.T) {
	_, s, db := setup(t)
	access := &agentaccess.Store{DB: db}
	_, secret, _ := access.Issue(ctx, "", "Agent", agentaccess.ActionRequest(), time.Hour)
	h := NewAgentHTTP(s, access)
	req := httptest.NewRequest("GET", AgentPrefix+"/providers/tachyon/verbs/actions.catalog?input=%7B%7D", nil)
	req.Header.Set("Authorization", "Bearer "+secret)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 404 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
}

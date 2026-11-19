package dataverbs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hollis-labs/tachyon/internal/agentaccess"
	"github.com/hollis-labs/tachyon/internal/chatplan"
	"github.com/hollis-labs/tachyon/internal/tesseractread"
	"github.com/hollis-labs/tachyon/internal/torqueread"
)

func TestNetworkMCPDoesNotRetainOrShareAuthority(t *testing.T) {
	_, s, db := setup(t)
	access := &agentaccess.Store{DB: db}
	writer, secret, err := access.Issue(ctx, "", "Architect", agentaccess.PortfolioWrite(), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	_, reader, err := access.Issue(ctx, "", "Reader", agentaccess.PortfolioRead(), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	handler := NewAgentMCP(s, access)
	call := func(method string, params any, credential, origin string, status int) string {
		t.Helper()
		raw, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest("POST", "https://tachyon.test"+AgentMCPPath, strings.NewReader(string(raw)))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		req.Header.Set("MCP-Protocol-Version", "2025-03-26")
		req.Header.Set("Mcp-Session-Id", "reused-across-principals")
		req.Header.Set("Cookie", "tachyon_approvals=forged")
		if credential != "" {
			req.Header.Set("Authorization", "Bearer "+credential)
		}
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != status {
			t.Fatalf("%s: %d: %s", method, w.Code, w.Body.String())
		}
		return w.Body.String()
	}
	call("tools/list", map[string]any{}, "", "", 401)
	call("tools/list", map[string]any{}, secret, "https://elsewhere.test", 403)
	initialize := call("initialize", map[string]any{"protocolVersion": "2025-03-26", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "test", "version": "1"}}, secret, "", 200)
	if !strings.Contains(initialize, "tachyon-planner") {
		t.Fatal(initialize)
	}
	discovery := call("tools/list", map[string]any{}, secret, "", 200)
	if !strings.Contains(discovery, "tachyon_planner_portfolio_patch") || strings.Contains(discovery, "tachyon_objects_delete") {
		t.Fatal(discovery)
	}
	discovery = call("tools/list", map[string]any{}, reader, "", 200)
	if strings.Contains(discovery, "tachyon_planner_portfolio_patch") {
		t.Fatal("writer tools leaked to reader")
	}
	id := uuid.NewString()
	params := map[string]any{"name": "tachyon_planner_portfolio_create", "arguments": map[string]any{"db": "ideas", "id": id, "fields": map[string]any{"title": "Network MCP"}}}
	created := call("tools/call", params, secret, "", 200)
	if !strings.Contains(created, "service:"+writer.IdentityID) {
		t.Fatal(created)
	}
	record, err := s.Get(ctx, id)
	if err != nil || record.Name != "Network MCP" {
		t.Fatal(record, err)
	}
	params["arguments"].(map[string]any)["id"] = uuid.NewString()
	refused := call("tools/call", params, reader, "", 200)
	if !strings.Contains(refused, "error") {
		t.Fatal("reader borrowed writer session", refused)
	}
	if err := access.Revoke(ctx, writer.ID, false); err != nil {
		t.Fatal(err)
	}
	call("tools/call", params, secret, "", 401)
}

// Nanite's MCP client (plugin-mcp, go-sdk v1.8.0) probes with server/discover before
// initialize. A stateless server that answers an unknown REQUEST (one with an id)
// with a body-less 202 and no Content-Type breaks that handshake; 202 belongs to
// notifications only. This replays the client's sequence and pins the shapes.
func TestAgentMCPHandshakeShapeForNaniteClient(t *testing.T) {
	_, s, db := setup(t)
	access := &agentaccess.Store{DB: db}
	_, secret, err := access.Issue(ctx, "", "Reader", agentaccess.PlannerRead(), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	handler := NewAgentMCP(s, access)
	do := func(method string, body any) *httptest.ResponseRecorder {
		t.Helper()
		var rd *strings.Reader
		if body != nil {
			raw, err := json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
			rd = strings.NewReader(string(raw))
		} else {
			rd = strings.NewReader("")
		}
		req := httptest.NewRequest(method, "https://tachyon.test"+AgentMCPPath, rd)
		req.Header.Set("Authorization", "Bearer "+secret)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		return w
	}
	rpc := func(id any, method string) map[string]any {
		m := map[string]any{"jsonrpc": "2.0", "method": method}
		if id != nil {
			m["id"] = id
		}
		return m
	}

	// server/discover: a JSON-RPC -32601 error with a JSON body (never a bare 202).
	w := do("POST", rpc(1, "server/discover"))
	if w.Code == 202 || !strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") || !strings.Contains(w.Body.String(), "-32601") {
		t.Fatalf("server/discover: %d %q %s", w.Code, w.Header().Get("Content-Type"), w.Body.String())
	}
	// Any other unknown request with an id is also never a body-less 202.
	if w = do("POST", rpc(2, "nope/nothing")); w.Code == 202 {
		t.Fatalf("unknown request answered 202: %s", w.Body.String())
	}
	// initialize: JSON, stateless (no session id).
	w = do("POST", map[string]any{"jsonrpc": "2.0", "id": 3, "method": "initialize", "params": map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "nanite-shape", "version": "1"}}})
	if w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") || w.Header().Get("Mcp-Session-Id") != "" {
		t.Fatalf("initialize: %d %q session=%q %s", w.Code, w.Header().Get("Content-Type"), w.Header().Get("Mcp-Session-Id"), w.Body.String())
	}
	// The real notification is the one that gets 202.
	if w = do("POST", rpc(nil, "notifications/initialized")); w.Code != 202 {
		t.Fatalf("notifications/initialized: %d %s", w.Code, w.Body.String())
	}
	// No standalone stream and no session to delete in stateless mode.
	if w = do("GET", nil); w.Code != 405 {
		t.Fatalf("GET: %d", w.Code)
	}
	if w = do("DELETE", nil); w.Code != 405 {
		t.Fatalf("DELETE: %d", w.Code)
	}
	// tools/list still works after the discovery probe.
	if w = do("POST", rpc(4, "tools/list")); w.Code != 200 || !strings.Contains(w.Body.String(), "tachyon_planner_list") {
		t.Fatalf("tools/list: %d %s", w.Code, w.Body.String())
	}
}

// Torque reads are an opt-in grant: a planner-read credential must not see the
// tools, and a credential without the Torque row kinds cannot call them.
func TestAgentMCPTorqueToolsAreOptInAndKindGated(t *testing.T) {
	_, s, db := setup(t)
	torque := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Errorf("non-GET to Torque: %s", r.Method)
		}
		switch {
		case r.URL.Path == "/api/v1/tasks/CW-9":
			json.NewEncoder(w).Encode(map[string]any{"id": "CW-9", "kind": "agent", "title": "Nine", "status": "todo", "priority": 1, "description": "d", "updated_at": "2026-10-24T00:00:00Z", "system_prompt": "NOPE", "environment": "NOPE"})
		case r.URL.Path == "/api/v1/tasks/CW-9/comments":
			json.NewEncoder(w).Encode(map[string]any{"items": []any{map[string]any{"id": 1, "author": "a", "content": "hello", "created_at": "2026-10-24T00:00:00Z"}}, "meta": map[string]any{"has_more": false}})
		case r.URL.Path == "/api/v1/tasks":
			json.NewEncoder(w).Encode(map[string]any{"items": []any{map[string]any{"id": "CW-9", "kind": "agent", "title": "Nine", "status": "todo"}}, "meta": map[string]any{"has_more": false}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer torque.Close()
	access := &agentaccess.Store{DB: db}
	_, plain, err := access.Issue(ctx, "", "Planner", agentaccess.PlannerRead(), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	_, withTorque, err := access.Issue(ctx, "", "App agent", agentaccess.PlannerTorqueRead(), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	_, epicOnly, err := access.Issue(ctx, "", "Epic only", agentaccess.Grants{Verbs: []string{"torque.task.get"}, Kinds: []string{"epic"}}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	handler := NewAgentMCP(s, access, torqueread.New(torque.URL, ""))
	call := func(method string, params any, credential string) string {
		t.Helper()
		raw, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
		req := httptest.NewRequest("POST", "https://tachyon.test"+AgentMCPPath, strings.NewReader(string(raw)))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		req.Header.Set("Authorization", "Bearer "+credential)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		return w.Body.String()
	}
	if listed := call("tools/list", map[string]any{}, plain); strings.Contains(listed, "tachyon_torque_") || !strings.Contains(listed, "tachyon_planner_list") {
		t.Fatalf("planner-read token sees Torque tools or lost its own: %s", listed)
	}
	listed := call("tools/list", map[string]any{}, withTorque)
	for _, tool := range []string{"tachyon_torque_task_get", "tachyon_torque_task_comments", "tachyon_torque_task_list", "tachyon_planner_list"} {
		if !strings.Contains(listed, tool) {
			t.Fatalf("missing %s: %s", tool, listed)
		}
	}
	if strings.Contains(listed, "tachyon_objects_delete") || strings.Contains(listed, "write") {
		t.Fatalf("write-capable tool listed: %s", listed)
	}
	got := call("tools/call", map[string]any{"name": "tachyon_torque_task_get", "arguments": map[string]any{"id": "CW-9"}}, withTorque)
	if !strings.Contains(got, `\"title\":\"Nine\"`) || !strings.Contains(got, "untrusted_data") || strings.Contains(got, "NOPE") {
		t.Fatalf("task.get: %s", got)
	}
	if got := call("tools/call", map[string]any{"name": "tachyon_torque_task_comments", "arguments": map[string]any{"id": "CW-9"}}, withTorque); !strings.Contains(got, "hello") {
		t.Fatalf("task.comments: %s", got)
	}
	if got := call("tools/call", map[string]any{"name": "tachyon_torque_task_list", "arguments": map[string]any{"status": "todo"}}, withTorque); !strings.Contains(got, "CW-9") {
		t.Fatalf("task.list: %s", got)
	}
	if got := call("tools/call", map[string]any{"name": "tachyon_torque_task_get", "arguments": map[string]any{"id": "CW-404"}}, withTorque); !strings.Contains(got, "not_found") && !strings.Contains(got, "No such Torque record") {
		t.Fatalf("missing record not reported as not found: %s", got)
	}
	if got := call("tools/call", map[string]any{"name": "tachyon_torque_task_get", "arguments": map[string]any{"id": "../x"}}, withTorque); !strings.Contains(got, "isError") {
		t.Fatalf("bad id accepted: %s", got)
	}
	// A credential with the verb but not the task/plan kinds, and one without the verb, are refused.
	if got := call("tools/call", map[string]any{"name": "tachyon_torque_task_get", "arguments": map[string]any{"id": "CW-9"}}, epicOnly); strings.Contains(got, "Nine") {
		t.Fatalf("kind gate bypassed: %s", got)
	}
	if got := call("tools/call", map[string]any{"name": "tachyon_torque_task_get", "arguments": map[string]any{"id": "CW-9"}}, plain); strings.Contains(got, "Nine") {
		t.Fatalf("planner-read token reached Torque: %s", got)
	}
}

// Tesseract reads are an opt-in grant needing no object kinds, and the namespace
// allowlist applies even to a credential that holds the verbs.
func TestAgentMCPTesseractToolsAreOptInAndNamespaceGated(t *testing.T) {
	_, s, db := setup(t)
	tess := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "POST" && r.URL.Path == "/v1/tesseract/lookup":
			json.NewEncoder(w).Encode(map[string]any{"results": []any{map[string]any{"score": 0.5, "revision": map[string]any{
				"item_id": "i1", "namespace": "project/tachyon/memory/decisions", "memory_key": "embedded_agent", "domain": "memory", "status": "canonical", "tags": []string{"d"},
				"payload": map[string]any{"summary": "the summary"}}}}, "manifest": map[string]any{"results_total": 1}})
		case r.Method == "GET" && r.URL.Path == "/v1/items/i1":
			json.NewEncoder(w).Encode(map[string]any{"item_id": "i1", "namespace": "project/tachyon/memory/decisions", "memory_key": "embedded_agent", "domain": "memory",
				"payload": map[string]any{"summary": "the summary", "body": "the full body"}, "session_id": "SESSION-LEAK"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer tess.Close()
	reader, err := tesseractread.New(tess.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	access := &agentaccess.Store{DB: db}
	issue := func(name string, g agentaccess.Grants) string {
		_, secret, err := access.Issue(ctx, "", name, g, time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		return secret
	}
	plain := issue("Planner", agentaccess.PlannerTorqueRead())
	only := issue("Tesseract only", agentaccess.TesseractRead())
	app := issue("App agent", agentaccess.AppAgentRead())
	handler := NewAgentMCP(s, access, torqueread.New("http://127.0.0.1:1", ""), reader)
	call := func(method string, params any, credential string) string {
		t.Helper()
		raw, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
		req := httptest.NewRequest("POST", "https://tachyon.test"+AgentMCPPath, strings.NewReader(string(raw)))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		req.Header.Set("Authorization", "Bearer "+credential)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		return w.Body.String()
	}
	if l := call("tools/list", map[string]any{}, plain); strings.Contains(l, "tachyon_tesseract_") {
		t.Fatalf("a planner+torque token sees Tesseract tools: %s", l)
	}
	l := call("tools/list", map[string]any{}, only)
	if !strings.Contains(l, "tachyon_tesseract_recall") || !strings.Contains(l, "tachyon_tesseract_get") || strings.Contains(l, "tachyon_planner_list") || strings.Contains(l, "tachyon_torque_") {
		t.Fatalf("tesseract-only token: %s", l)
	}
	l = call("tools/list", map[string]any{}, app)
	for _, tool := range []string{"tachyon_planner_list", "tachyon_objects_get", "tachyon_torque_task_get", "tachyon_torque_task_comments", "tachyon_torque_task_list", "tachyon_tesseract_recall", "tachyon_tesseract_get"} {
		if !strings.Contains(l, tool) {
			t.Fatalf("app-agent token lacks %s: %s", tool, l)
		}
	}
	if strings.Contains(l, "portfolio_create") || strings.Contains(l, "actions_request") {
		t.Fatalf("write tools listed: %s", l)
	}
	got := call("tools/call", map[string]any{"name": "tachyon_tesseract_recall", "arguments": map[string]any{"namespaces": []string{"project/tachyon/memory/decisions"}, "query": "agent"}}, app)
	if !strings.Contains(got, "embedded_agent") || !strings.Contains(got, "untrusted_data") || strings.Contains(got, "the full body") {
		t.Fatalf("recall: %s", got)
	}
	got = call("tools/call", map[string]any{"name": "tachyon_tesseract_get", "arguments": map[string]any{"item_id": "i1"}}, app)
	if !strings.Contains(got, "the full body") || strings.Contains(got, "SESSION-LEAK") {
		t.Fatalf("get: %s", got)
	}
	got = call("tools/call", map[string]any{"name": "tachyon_tesseract_recall", "arguments": map[string]any{"namespaces": []string{"user/chrispian/memory"}}}, app)
	if !strings.Contains(got, "isError") || !strings.Contains(got, "project/ and system/") {
		t.Fatalf("private namespace not refused: %s", got)
	}
	if got := call("tools/call", map[string]any{"name": "tachyon_tesseract_get", "arguments": map[string]any{"item_id": "i1"}}, plain); strings.Contains(got, "the full body") {
		t.Fatalf("token without the verb reached Tesseract: %s", got)
	}
}

// Tether's tachyon_plan upstream calls the MCP route, so the X-Tether-* headers must count there
// exactly as on the HTTP route: lineage is recorded for the pinned proxy, a replacement session
// gets its predecessor's plan on its first plan_get, and any other caller's headers are ignored.
func TestAgentMCPRecordsLineageAndCarriesPlanForPinnedProxy(t *testing.T) {
	_, s, db := setup(t)
	access := &agentaccess.Store{DB: db}
	proxyTok, proxy, err := access.Issue(ctx, "", "tether-mcp-proxy", agentaccess.ChatPlan(), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err = access.PinProxy(ctx, proxyTok.IdentityID); err != nil {
		t.Fatal(err)
	}
	_, direct, err := access.Issue(ctx, "", "Direct agent", agentaccess.ChatPlan(), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	svc := &chatplan.Service{Store: &chatplan.Store{DB: db}, Exists: func(context.Context, string) error { return nil }}
	known := func(ctx context.Context, ref string) (ChatThread, error) {
		for _, st := range svc.Store.Sessions(ctx) {
			if st.Ref == ref {
				return ChatThread{Ref: ref, AgentID: st.Agent}, nil
			}
		}
		return ChatThread{}, errors.New("none")
	}
	plan := &ChatPlan{Service: svc, Thread: known, Threads: func(context.Context) ([]ChatThread, error) { return nil, nil }}
	handler := NewAgentMCP(s, access, plan)
	call := func(credential, session, agent, continues, tool, args string) string {
		t.Helper()
		raw, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": tool, "arguments": json.RawMessage(args)}})
		req := httptest.NewRequest("POST", "https://tachyon.test"+AgentMCPPath, strings.NewReader(string(raw)))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		req.Header.Set("Authorization", "Bearer "+credential)
		for k, v := range map[string]string{SessionHeader: session, LogicalAgentHeader: agent, ContinuesHeader: continues} {
			if v != "" {
				req.Header.Set(k, v)
			}
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != 200 {
			t.Fatalf("%s: %d: %s", tool, w.Code, w.Body.String())
		}
		return w.Body.String()
	}
	// row reports the lineage recorded for a session: its agent and the session it continues.
	row := func(session string) (agent, continues string, ok bool) {
		t.Helper()
		var cont sql.NullString
		err := db.Reader.QueryRowContext(ctx, `SELECT agent_id,continues_id FROM chat_session_lineage WHERE session_id=?`, session).Scan(&agent, &cont)
		if errors.Is(err, sql.ErrNoRows) {
			return "", "", false
		}
		if err != nil {
			t.Fatal(err)
		}
		return agent, cont.String, true
	}

	// s1 writes a plan through the proxy before it has a single turn; its lineage row is written.
	if out := call(proxy, "s1", "worker", "", "tachyon_chat_plan_write", `{"title":"Ship","steps":[{"title":"one"}]}`); strings.Contains(out, "isError") || !strings.Contains(out, `\"revision\":1`) {
		t.Fatalf("proxied session could not write its plan: %s", out)
	}
	if agent, continues, ok := row("s1"); !ok || agent != "worker" || continues != "" {
		t.Fatalf("s1 lineage = %q continues %q (recorded %v)", agent, continues, ok)
	}

	// A direct credential forging all three headers gets no session, no lineage row and no plan.
	if out := call(direct, "s2", "worker", "s1", "tachyon_chat_plan_get", `{}`); !strings.Contains(out, "isError") || strings.Contains(out, "Ship") {
		t.Fatalf("forged continuation from a non-proxy caller was served: %s", out)
	}
	if out := call(direct, "s1", "worker", "", "tachyon_chat_plan_get", `{}`); !strings.Contains(out, "isError") || strings.Contains(out, "Ship") {
		t.Fatalf("forged session id from a non-proxy caller was served: %s", out)
	}
	if _, _, ok := row("s2"); ok {
		t.Fatal("a non-proxy caller wrote a lineage row")
	}

	// Lineage is recorded on any proxied request, not only a tool call.
	raw := `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`
	req := httptest.NewRequest("POST", "https://tachyon.test"+AgentMCPPath, strings.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Authorization", "Bearer "+proxy)
	req.Header.Set(SessionHeader, "s0")
	req.Header.Set(LogicalAgentHeader, "worker")
	handler.ServeHTTP(httptest.NewRecorder(), req)
	if _, _, ok := row("s0"); !ok {
		t.Fatal("tools/list through the proxy did not record the session")
	}

	// The replacement session gets the plan on its first plan_get, moved once.
	out := call(proxy, "s2", "worker", "s1", "tachyon_chat_plan_get", `{}`)
	if strings.Contains(out, "isError") || !strings.Contains(out, `\"title\":\"Ship\"`) || !strings.Contains(out, `\"revision\":2`) {
		t.Fatalf("replacement session did not receive the plan: %s", out)
	}
	if agent, continues, ok := row("s2"); !ok || agent != "worker" || continues != "s1" {
		t.Fatalf("s2 lineage = %q continues %q (recorded %v)", agent, continues, ok)
	}
	if out := call(proxy, "s1", "worker", "", "tachyon_chat_plan_get", `{}`); !strings.Contains(out, "isError") || strings.Contains(out, "Ship") {
		t.Fatalf("the old session still holds the plan: %s", out)
	}
}

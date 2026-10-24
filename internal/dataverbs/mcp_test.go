package dataverbs

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hollis-labs/tachyon/internal/agentaccess"
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

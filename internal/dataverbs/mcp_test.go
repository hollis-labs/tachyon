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

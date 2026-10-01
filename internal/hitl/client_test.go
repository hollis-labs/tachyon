package hitl

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	core "github.com/hollis-labs/go-hitl"
	"github.com/hollis-labs/tachyon/internal/contract"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var contractAsk = contract.AskDetail{Prompt: "Proceed?"}

func TestMCPEnqueueLostResponseAndReadTools(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "fake-tangent", Version: "1"}, nil)
	schema := requestSchema(t)
	var mu sync.Mutex
	var requests [][]byte
	var reads []string
	for _, name := range []string{"tangent.hitl_enqueue", "tangent.hitl_get", "tangent.hitl_await"} {
		server.AddTool(&mcp.Tool{Name: name, InputSchema: json.RawMessage(`{"type":"object"}`)}, func(_ context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			mu.Lock()
			defer mu.Unlock()
			if req.Params.Name == "tangent.hitl_enqueue" {
				if e := validateRequestBytes(schema, req.Params.Arguments); e != nil {
					t.Errorf("fake Tangent rejected request: %v", e)
					return &mcp.CallToolResult{IsError: true}, nil
				}
				requests = append(requests, append([]byte(nil), req.Params.Arguments...))
				if len(requests) == 1 {
					return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "lost/malformed response"}}}, nil
				}
				return &mcp.CallToolResult{StructuredContent: EnqueueHandle{ContractVersion: "1.0", ItemID: "item", State: "submitted", Revision: 1}}, nil
			}
			var cmd core.GetCommand
			if e := json.Unmarshal(req.Params.Arguments, &cmd); e != nil || cmd.Caller.PrincipalRef != "tachyon-host" || cmd.Caller.ApplicationID != "tachyon" {
				t.Errorf("bad command %s", req.Params.Arguments)
			}
			reads = append(reads, req.Params.Name)
			mode, wait := "get", "not_waited"
			if req.Params.Name == "tangent.hitl_await" {
				mode, wait = "await", "timeout"
			}
			return &mcp.CallToolResult{StructuredContent: core.RetrievalResult{ContractVersion: "1.0", Mode: mode, WaitStatus: wait, RetrievedAt: time.Now(), Item: core.ItemView{ContractVersion: "1.0", ItemID: "item", State: core.StateSubmitted, Revision: 1, RequestSnapshot: json.RawMessage(`{}`)}}}, nil
		})
	}
	ts := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: true}))
	defer ts.Close()
	client, e := NewMCPClient(ts.URL)
	if e != nil {
		t.Fatal(e)
	}
	req := FromAskDetail("work_write", "tachyon:epoch:operation:1", &contractAsk)
	req.IdempotencyKey = "tachyon:epoch:operation:1"
	h, e := client.Enqueue(context.Background(), req)
	if e != nil || h.ItemID != "item" {
		t.Fatal(h, e)
	}
	mu.Lock()
	if len(requests) != 2 || string(requests[0]) != string(requests[1]) {
		t.Fatal("ambiguous retry changed request")
	}
	mu.Unlock()
	if _, e = client.Retrieve(context.Background(), "item", 0); e != nil {
		t.Fatal(e)
	}
	if _, e = client.Retrieve(context.Background(), "item", 10); e != nil {
		t.Fatal(e)
	}
	if _, e = client.Retrieve(context.Background(), "item", 50001); e == nil {
		t.Fatal("unbounded await")
	}
}
func TestMCPDeadline(t *testing.T) {
	stop := make(chan struct{})
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-stop:
		}
	}))
	defer ts.Close()
	defer func() { close(stop) }()
	c, _ := NewMCPClient(ts.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	started := time.Now()
	if _, e := c.Enqueue(ctx, FromAskDetail("verb", "key", &contractAsk)); e == nil {
		t.Fatal("timeout accepted")
	}
	if time.Since(started) > time.Second {
		t.Fatal("client deadline not bounded")
	}
}

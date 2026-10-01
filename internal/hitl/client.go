package hitl

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	core "github.com/hollis-labs/go-hitl"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const clientTimeout = 10 * time.Second // Includes MCP initialization, call, and cleanup.
const awaitTimeout = 55 * time.Second  // Tangent's maximum wait is 50s, plus transport overhead.
const maxResponse = 256 << 10          // Bound both JSON and SSE tool responses.

var errRejected = errors.New("Tangent rejected HITL request")

type Client interface {
	Enqueue(context.Context, EnqueueRequest) (EnqueueHandle, error)
	Retrieve(context.Context, string, int) (core.RetrievalResult, error)
}
type MCPClient struct {
	endpoint string
	http     *http.Client
}

// NewMCPClient is inert until a tool is called. Only operator configuration sets endpoints.
func NewMCPClient(endpoint string) (*MCPClient, error) {
	u, e := url.Parse(endpoint)
	if e != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.Fragment != "" {
		return nil, errors.New("invalid Tangent MCP URL")
	}
	return &MCPClient{endpoint: endpoint, http: &http.Client{Transport: limitedTransport{base: http.DefaultTransport}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

type limitedTransport struct {
	base http.RoundTripper
	ctx  context.Context
}

func (t limitedTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if t.ctx != nil {
		r = r.WithContext(t.ctx)
	}
	res, e := t.base.RoundTrip(r)
	if e == nil {
		res.Body = &limitedBody{Reader: io.LimitReader(res.Body, maxResponse+1), Closer: res.Body}
	}
	return res, e
}

type limitedBody struct {
	io.Reader
	io.Closer
}

func (c *MCPClient) call(ctx context.Context, name string, args any, out any, budget time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	httpClient := *c.http
	httpClient.Transport = limitedTransport{base: http.DefaultTransport, ctx: ctx}
	client := mcp.NewClient(&mcp.Implementation{Name: "tachyon-hitl", Version: "1.0"}, nil)
	session, e := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: c.endpoint, HTTPClient: &httpClient, DisableStandaloneSSE: true, MaxRetries: -1, MaxEventSize: maxResponse}, nil)
	if e != nil {
		return e
	}
	defer session.Close()
	res, e := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if e != nil {
		return e
	}
	if res.IsError {
		return errRejected
	}
	var raw []byte
	// Tangent's JSON text companion preserves numbers in request_snapshot;
	// StructuredContent is decoded into interface values by the MCP SDK.
	if len(res.Content) == 1 {
		if t, ok := res.Content[0].(*mcp.TextContent); ok && json.Valid([]byte(t.Text)) {
			raw = []byte(t.Text)
		}
	}
	if len(raw) == 0 && res.StructuredContent != nil {
		raw, e = json.Marshal(res.StructuredContent)
	}
	if e != nil {
		return e
	}
	if len(raw) == 0 || len(raw) > maxResponse {
		return errors.New("invalid HITL tool response")
	}
	return json.Unmarshal(raw, out)
}
func (c *MCPClient) Enqueue(ctx context.Context, req EnqueueRequest) (EnqueueHandle, error) {
	var h EnqueueHandle
	frozen, e := json.Marshal(req)
	if e != nil {
		return h, e
	}
	var typed core.EnqueueRequest
	if e = json.Unmarshal(frozen, &typed); e != nil {
		return h, e
	}
	if e = typed.Validate(); e != nil {
		return h, e
	}
	// One retry of the exact frozen request/key is safe even if the first response was lost.
	ctx, cancel := context.WithTimeout(ctx, 2*clientTimeout)
	defer cancel()
	e = c.call(ctx, "tangent.hitl_enqueue", json.RawMessage(frozen), &h, clientTimeout)
	if e != nil && ctx.Err() == nil && !errors.Is(e, errRejected) {
		e = c.call(ctx, "tangent.hitl_enqueue", json.RawMessage(frozen), &h, clientTimeout)
	}
	if e == nil && (h.ContractVersion != "1.0" || h.ItemID == "" || len(h.ItemID) > 256 || h.Revision < 1 || !core.State(h.State).Valid()) {
		e = errors.New("invalid enqueue handle")
	}
	return h, e
}
func (c *MCPClient) Retrieve(ctx context.Context, id string, wait int) (core.RetrievalResult, error) {
	var r core.RetrievalResult
	caller := core.CallerAssertion{ApplicationID: DefaultSource.ApplicationID, PrincipalRef: DefaultSource.AgentID}
	var e error
	if wait == 0 {
		e = c.call(ctx, "tangent.hitl_get", core.GetCommand{ContractVersion: "1.0", ItemID: id, Caller: caller}, &r, clientTimeout)
	} else {
		cmd := core.AwaitCommand{ContractVersion: "1.0", ItemID: id, Caller: caller, WaitMs: &wait}
		if e = cmd.Validate(); e != nil {
			return r, e
		}
		e = c.call(ctx, "tangent.hitl_await", cmd, &r, awaitTimeout)
	}
	if e == nil {
		e = r.Validate()
		expectedMode := core.ModeGet
		if wait != 0 {
			expectedMode = core.ModeAwait
		}
		if e == nil && r.Mode != expectedMode {
			e = errors.New("wrong retrieval mode")
		}
	}
	if e == nil && (r.ContractVersion != "1.0" || r.Item.ContractVersion != "1.0" || !r.Item.State.Valid() || r.Item.Revision < 1 || r.Item.ItemID != id) {
		e = fmt.Errorf("invalid HITL retrieval identity")
	}
	return r, e
}

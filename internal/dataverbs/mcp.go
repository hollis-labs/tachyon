package dataverbs

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	mcpserver "github.com/hollis-labs/libs/mcp/plugin-mcp/go-mcp/server"
	"github.com/hollis-labs/libs/security/auth"
	"github.com/hollis-labs/tachyon/internal/objects"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const AgentMCPPath = "/api/agent/v1/mcp"

// NewAgentMCP exposes the same scoped provider as the agent HTTP API. Stateless
// requests reauthenticate and rediscover grants each time; an MCP session cannot
// retain a revoked credential or borrow another caller's tool set.
func NewAgentMCP(s *objects.Service, verifier auth.Verifier, services ...any) http.Handler {
	svc, cardSvc, torque, tess, chat, topics, plan := agentServices(services)
	guard, err := auth.New(auth.Config{Mode: auth.ModeEnforce, Verifier: verifier, OpenPath: func(string) bool { return false }})
	if err != nil {
		panic(err)
	}
	handler := mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		principal, ok := auth.FromContext(r.Context())
		if !ok || !principal.Verified {
			return nil
		}
		p := &scopedLocal{local: NewLocal(s), principal: principal, actions: svc, cards: cardSvc, torque: torque, tesseract: tess, chat: chat, topics: topics, plan: plan}
		server := mcpserver.NewServer("tachyon-planner", "1.0.0")
		for _, v := range p.Verbs() {
			server.RegisterTool(mcpserver.Tool{
				Name:        "tachyon_" + strings.ReplaceAll(v.Name, ".", "_"),
				Description: v.Description, InputSchema: v.InputSchema,
				ReadOnlyHint: !v.Mutates, DestructiveHint: v.Mutates, IdempotentHint: !v.Mutates,
				Handler: func(ctx context.Context, args map[string]any) (any, error) {
					raw, err := json.Marshal(args)
					if err != nil {
						return nil, err
					}
					result, err := p.Invoke(ctx, v.Name, raw, "")
					if err != nil {
						return nil, err
					}
					return response{Version: ContractVersion, Provider: p.ID(), Verb: v.Name, Data: result}, nil
				},
			})
		}
		return server.SDKServer()
	}, &mcp.StreamableHTTPOptions{
		Stateless: true, JSONResponse: true, MaxRequestBodyBytes: MaxInputBytes,
		// The public HTTPS reverse proxy connects to a loopback listener. Every
		// request requires an explicit bearer (never cookies/ambient authority),
		// and cross-origin browser requests are separately rejected below.
		DisableLocalhostProtection: true,
	})
	protected := http.NewCrossOriginProtection().Handler(guard.Middleware(handler))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		protected.ServeHTTP(w, r)
	})
}

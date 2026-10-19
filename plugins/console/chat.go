package main

import (
	"context"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/hollis-labs/libs/mcp/plugin-mcp/plugin-sdk/subprocess"
)

const chatPrefix = "/api/plugins/console/v1/chat"

var chatSendPath = regexp.MustCompile(`^/agents/[a-zA-Z0-9_-]+/send$`)

// Chat uses the same reviewed Tether credential transport as the catalog.
// Only the chat roster and a single launch's send operation are exposed.
type ChatHandler struct{ tether *CatalogHandler }

func NewChatHandler(addr, tokenFile string) *ChatHandler {
	h := NewCatalogHandler(addr, tokenFile)
	if h.proxy != nil {
		h.proxy.Routes[0].Prefix = "/v1/chat"
		h.proxy.Routes[0].Target = "/chat"
		h.proxy.Client.Timeout = 25 * time.Second
	}
	return &ChatHandler{tether: h}
}
func (*ChatHandler) Match(path string) bool { return strings.HasPrefix(path, chatPrefix+"/") }
func (h *ChatHandler) Handle(ctx context.Context, req subprocess.HTTPRequest) subprocess.HTTPResponse {
	path := strings.TrimPrefix(req.Path, chatPrefix)
	if !(req.Method == http.MethodGet && path == "/agents") && !(req.Method == http.MethodPost && chatSendPath.MatchString(path)) {
		return forgeErr(404, "not_found", "Unknown chat operation")
	}
	if h.tether.err != nil || h.tether.proxy == nil {
		return forgeErr(503, "configuration", "Tether chat connection or credential is not configured")
	}
	// Do not let callers expand the roster to disabled launches.
	req.Query = nil
	req.RawQuery = ""
	res, _ := h.tether.proxy.HTTPHandle(ctx, req)
	return res
}

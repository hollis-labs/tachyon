package main

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/hollis-labs/libs/mcp/plugin-mcp/plugin-sdk/subprocess"
	"github.com/hollis-labs/tachyon/internal/upstreamauth"
	"github.com/hollis-labs/tachyon/pkg/pluginkit"
)

const catalogPrefix = "/api/plugins/console/v1/catalog"

// CatalogHandler exposes only the catalog reads needed by the builder.
// Save demonstrations stay in the browser and never reach Tether.
type CatalogHandler struct {
	proxy *pluginkit.Proxy
	err   error
}

func NewCatalogHandler(addr, tokenFile string) *CatalogHandler {
	h := &CatalogHandler{}
	if strings.TrimSpace(addr) == "" {
		return h
	}
	addr = strings.TrimSpace(addr)
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxResponseHeaderBytes = 64 << 10
	if socket, ok := strings.CutPrefix(addr, "unix:"); ok {
		transport.Proxy = nil
		transport.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "unix", socket)
		}
		addr = "http://localhost"
	} else if rest, ok := strings.CutPrefix(addr, "tcp:"); ok {
		addr = "http://" + rest
	}
	u, err := url.Parse(addr)
	if err != nil || u.Host == "" || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") || u.RawQuery != "" || u.Fragment != "" {
		h.err = &url.Error{Op: "configure", URL: "Tether", Err: errCatalogAddress{}}
		return h
	}
	rt, err := upstreamauth.Wrap(transport, addr, tokenFile)
	if err != nil {
		h.err = err
		return h
	}
	h.proxy = pluginkit.NewProxy("console", pluginkit.Route{Prefix: "/v1/catalog", Upstream: addr, Target: "/catalog", Headers: map[string]string{"Tether-Protocol": "1"}})
	h.proxy.Client = &http.Client{Transport: rt, Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return h
}

type errCatalogAddress struct{}

func (errCatalogAddress) Error() string { return "expected a Tether HTTP(S), TCP or unix address" }

func (*CatalogHandler) Match(path string) bool {
	return path == catalogPrefix || strings.HasPrefix(path, catalogPrefix+"/")
}

func (h *CatalogHandler) Handle(ctx context.Context, req subprocess.HTTPRequest) subprocess.HTTPResponse {
	if req.Method != http.MethodGet {
		return forgeErr(405, "read_only", "Builder writes are demonstration no-ops pending write access")
	}
	switch strings.TrimPrefix(req.Path, catalogPrefix) {
	case "/agents", "/launches", "/providers", "/projects":
	default:
		return forgeErr(404, "not_found", "Unknown catalog read")
	}
	if h.err != nil {
		return forgeErr(503, "configuration", "Tether address or credential transport is invalid")
	}
	if h.proxy == nil {
		return forgeErr(503, "configuration", "Configure TETHER_ADDR and, for authenticated access, TETHER_TOKEN_FILE on the Tachyon host")
	}
	res, _ := h.proxy.HTTPHandle(ctx, req)
	res.Headers["Cache-Control"] = "no-store"
	return res
}

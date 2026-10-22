// Command console is Tachyon's Console plugin backend: agent fleet, builder
// and the operator console overlay. Fleet activity comes directly from the
// activity monitor. Backends that do not exist yet answer 404 from upstream;
// see docs/data-gaps.md.
package main

import (
	"context"
	"net/http"
	"os"
	"strings"

	"github.com/hollis-labs/libs/mcp/plugin-mcp/plugin-sdk/subprocess"

	"github.com/hollis-labs/tachyon/internal/upstreamauth"
	"github.com/hollis-labs/tachyon/pkg/pluginkit"
)

const version = "0.1.0"

type console struct {
	proxy     *pluginkit.Proxy
	forge     *ForgeHandler
	catalog   *CatalogHandler
	tesseract *TesseractHandler
	chat      *ChatHandler
	objects   *ObjectHandler
}

func (console) Init(context.Context, subprocess.InitParams) (subprocess.InitResult, error) {
	return subprocess.InitResult{
		ID:                 "console",
		Name:               "Console",
		Version:            version,
		Description:        "Operator console: agent fleet, builder, and GitHub forge",
		Protocol:           subprocess.ProtocolVersion,
		CapabilityContract: 1,
	}, nil
}

func (console) Load(context.Context) (subprocess.LoadResult, error) {
	return subprocess.LoadResult{}, nil
}
func (console) Unload(context.Context) error { return nil }

func (c console) HTTPHandle(ctx context.Context, req subprocess.HTTPRequest) (subprocess.HTTPResponse, error) {
	if strings.HasPrefix(req.Path, "/api/plugins/console/v1/activity/") {
		if req.Path != "/api/plugins/console/v1/activity/agents" || req.Method != http.MethodGet {
			return subprocess.HTTPResponse{Status: http.StatusNotFound}, nil
		}
		return c.proxy.HTTPHandle(ctx, req)
	}
	if c.objects != nil && c.objects.Match(req.Path) {
		return c.objects.Handle(ctx, req), nil
	}
	if c.chat != nil && c.chat.Match(req.Path) {
		return c.chat.Handle(ctx, req), nil
	}
	if c.catalog != nil && c.catalog.Match(req.Path) {
		return c.catalog.Handle(ctx, req), nil
	}
	if c.tesseract != nil && c.tesseract.Match(req.Path) {
		return c.tesseract.Handle(ctx, req), nil
	}
	if c.forge.Match(req.Path) {
		return c.forge.Handle(ctx, req), nil
	}
	return c.proxy.HTTPHandle(ctx, req)
}

func main() {
	torque := pluginkit.Env("TORQUE_URL", "http://127.0.0.1:8990")
	proxy := pluginkit.NewProxy("console",
		pluginkit.Route{Prefix: "/v1/activity/agents", Upstream: pluginkit.Env("TACHYON_ACTIVITY_URL", "http://127.0.0.1:8099"), Target: "/api/plugins/activity-monitor/agents"},
		pluginkit.Route{Prefix: "/v1", Upstream: torque, Target: "/api/v1", Bearer: os.Getenv("TORQUE_API_TOKEN"), Headers: map[string]string{"X-Client": "browser"}},
	)
	forge := NewForgeHandler(os.Getenv("GITHUB_TOKEN"), pluginkit.Env("GITHUB_ORG", "hollis-labs"))
	if err := subprocess.Serve(console{proxy: proxy, forge: forge, objects: NewObjectHandler(os.Getenv("TETHER_ADDR"), upstreamauth.TokenFile(nil, "", "TETHER_TOKEN_FILE")), chat: NewChatHandler(os.Getenv("TETHER_ADDR"), upstreamauth.TokenFile(nil, "", "TETHER_TOKEN_FILE")), catalog: NewCatalogHandler(os.Getenv("TETHER_ADDR"), upstreamauth.TokenFile(nil, "", "TETHER_TOKEN_FILE")), tesseract: NewTesseractHandler(os.Getenv("TESSERACT_URL"), upstreamauth.TokenFile(nil, "", "TESSERACT_TOKEN_FILE"))}); err != nil {
		os.Exit(1)
	}
}

// History Ops is a stateless, read-only plugin over Fragments Engine's
// owner-only transcript routes: session history, search (text, summary,
// semantic, hybrid), facets and projects. Fragments owns all the data.
package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/hollis-labs/libs/plugin-mcp/plugin-sdk/subprocess"
	"github.com/hollis-labs/tachyon/internal/contract"
	"github.com/hollis-labs/tachyon/internal/pluginkit"
	"github.com/hollis-labs/tachyon/internal/upstreamauth"
)

//go:embed capabilities.json
var capabilitiesJSON []byte

type plugin struct{ api *fragmentsAPI }

func (*plugin) Capabilities() contract.PluginCapabilities {
	var caps contract.PluginCapabilities
	if err := json.Unmarshal(capabilitiesJSON, &caps); err != nil {
		panic(fmt.Sprintf("history-ops capabilities: %v", err))
	}
	return caps
}

func (p *plugin) Init(_ context.Context, params subprocess.InitParams) (subprocess.InitResult, error) {
	base := strings.TrimSpace(params.Config["fragments_url"])
	if base == "" {
		base = strings.TrimSpace(os.Getenv("FRAGMENTS_URL"))
	}
	api, err := newFragmentsAPI(base, upstreamauth.TokenFile(params.Config, "fragments_token_file", "FRAGMENTS_TOKEN_FILE"))
	if err != nil {
		return subprocess.InitResult{}, fmt.Errorf("configure Fragments: %w", err)
	}
	p.api = api
	return subprocess.InitResult{ID: "history-ops", Name: "History Ops", Version: "0.1.0", Description: "Read-only chat transcript history from Fragments Engine", CapabilityContract: 1, Protocol: subprocess.ProtocolVersion}, nil
}
func (*plugin) Load(context.Context) (subprocess.LoadResult, error) {
	return subprocess.LoadResult{}, nil
}
func (*plugin) Unload(context.Context) error { return nil }
func (p *plugin) Command(ctx context.Context, req subprocess.CommandRequest) (subprocess.CommandResult, error) {
	if result, handled, err := pluginkit.Dispatch(ctx, p, req); handled || err != nil {
		return result, err
	}
	return subprocess.CommandResult{}, fmt.Errorf("unknown command: %s", req.Name)
}
func main() {
	if err := subprocess.Serve(&plugin{}); err != nil {
		os.Exit(1)
	}
}

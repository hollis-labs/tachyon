// Catalog Ops is a stateless control-plane plugin over Tether's catalog
// (agents, launches, projects, providers) and registry. Tether owns all state.
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

// plugin is read-only until Init sees read_only=false: the zero value hides
// every verb that is not a pure read, so a host that asks for capabilities
// before (or without) configuration never advertises a write.
type plugin struct {
	api      *tetherAPI
	writable bool
}

// allCapabilities is every verb the plugin implements, writes included.
func allCapabilities() contract.PluginCapabilities {
	var caps contract.PluginCapabilities
	if err := json.Unmarshal(capabilitiesJSON, &caps); err != nil {
		panic(fmt.Sprintf("catalog-ops capabilities: %v", err))
	}
	return caps
}

// Capabilities advertises only the verbs this instance will run.
func (p *plugin) Capabilities() contract.PluginCapabilities {
	caps := allCapabilities()
	if p.writable {
		return caps
	}
	for verb, decl := range caps.Verbs {
		if decl.Effect != contract.EffectReads {
			delete(caps.Verbs, verb)
		}
	}
	return caps
}

// writesEnabled reads read_only from plugin config, then CATALOG_OPS_READ_ONLY.
// Only an explicit false/0/no turns writes on; anything else stays read-only.
func writesEnabled(config map[string]string) bool {
	v := strings.TrimSpace(config["read_only"])
	if v == "" {
		v = strings.TrimSpace(os.Getenv("CATALOG_OPS_READ_ONLY"))
	}
	switch strings.ToLower(v) {
	case "false", "0", "no":
		return true
	}
	return false
}

func (p *plugin) Init(_ context.Context, params subprocess.InitParams) (subprocess.InitResult, error) {
	addr := strings.TrimSpace(params.Config["tether_addr"])
	if addr == "" {
		addr = strings.TrimSpace(os.Getenv("TETHER_ADDR"))
	}
	api, err := newTetherAPI(addr, upstreamauth.TokenFile(params.Config, "tether_token_file", "TETHER_TOKEN_FILE"))
	if err != nil {
		return subprocess.InitResult{}, fmt.Errorf("configure Tether: %w", err)
	}
	p.api = api
	p.writable = writesEnabled(params.Config)
	return subprocess.InitResult{ID: "catalog-ops", Name: "Catalog Ops", Version: "0.1.0", Description: "Tether catalog and registry control plane", CapabilityContract: 1, Protocol: subprocess.ProtocolVersion}, nil
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

// Session Ops is a stateless Tether-backed session lifecycle plugin.
package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"

	"github.com/hollis-labs/plugin-sdk/subprocess"
	"github.com/hollis-labs/tachyon/internal/contract"
	"github.com/hollis-labs/tachyon/internal/pluginkit"
)

//go:embed capabilities.json
var capabilitiesJSON []byte

type plugin struct{ adapter SessionAdapter }

func (*plugin) Capabilities() contract.PluginCapabilities {
	var caps contract.PluginCapabilities
	if err := json.Unmarshal(capabilitiesJSON, &caps); err != nil {
		panic(fmt.Sprintf("session-ops capabilities: %v", err))
	}
	return caps
}

func (p *plugin) Init(_ context.Context, params subprocess.InitParams) (subprocess.InitResult, error) {
	addr := params.Config["tether_addr"]
	if addr == "" {
		addr = os.Getenv("TETHER_ADDR")
	}
	adapter, err := NewTetherAdapter(addr)
	if err != nil {
		return subprocess.InitResult{}, fmt.Errorf("configure Tether: %w", err)
	}
	p.adapter = adapter
	return subprocess.InitResult{ID: "session-ops", Name: "Session Ops", Version: "0.1.0", Description: "Tether-backed session lifecycle", Protocol: subprocess.ProtocolVersion}, nil
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

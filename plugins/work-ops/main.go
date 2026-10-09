// work-ops provides Torque-backed work tracking through the host verb contract.
package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"

	"github.com/hollis-labs/libs/plugin-mcp/plugin-sdk/subprocess"
	"github.com/hollis-labs/tachyon/internal/contract"
	"github.com/hollis-labs/tachyon/internal/pluginkit"
)

//go:embed capabilities.json
var capabilityJSON []byte

type plugin struct {
	adapter        WorkAdapter
	defaultProject string
}

func (*plugin) Capabilities() contract.PluginCapabilities {
	var caps contract.PluginCapabilities
	if json.Unmarshal(capabilityJSON, &caps) != nil {
		return contract.PluginCapabilities{}
	}
	return caps
}
func (p *plugin) Init(_ context.Context, params subprocess.InitParams) (subprocess.InitResult, error) {
	endpoint := "http://127.0.0.1:8990"
	if configured := params.Config["torque_url"]; configured != "" {
		endpoint = configured
	}
	p.adapter = NewTorqueAdapter(endpoint)
	p.defaultProject = params.Config["default_project"]
	return subprocess.InitResult{ID: "work-ops", Name: "Work Ops", Version: "0.1.0", Description: "Torque-backed work tracking", CapabilityContract: 1, Protocol: subprocess.ProtocolVersion}, nil
}
func (*plugin) Load(context.Context) (subprocess.LoadResult, error) {
	return subprocess.LoadResult{}, nil
}
func (*plugin) Unload(context.Context) error { return nil }
func (p *plugin) Command(ctx context.Context, req subprocess.CommandRequest) (subprocess.CommandResult, error) {
	if result, handled, err := pluginkit.Dispatch(ctx, p, req); handled {
		return result, err
	}
	return subprocess.CommandResult{}, fmt.Errorf("unknown command: %s", req.Name)
}

var _ pluginkit.VerbPlugin = (*plugin)(nil)

func main() {
	if subprocess.Serve(&plugin{}) != nil {
		os.Exit(1)
	}
}

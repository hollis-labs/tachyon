// Config Ops persists schema-validated plugin settings. Values apply on spawn.
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

type plugin struct{ adapter ConfigAdapter }

func (*plugin) Capabilities() contract.PluginCapabilities {
	var caps contract.PluginCapabilities
	if err := json.Unmarshal(capabilitiesJSON, &caps); err != nil {
		panic(err)
	}
	return caps
}
func (p *plugin) Init(_ context.Context, params subprocess.InitParams) (subprocess.InitResult, error) {
	dataDir, err := params.ResolvedDataDir()
	if err != nil {
		return subprocess.InitResult{}, err
	}
	adapter, err := NewLocalAdapter(dataDir)
	if err != nil {
		return subprocess.InitResult{}, err
	}
	p.adapter = adapter
	return subprocess.InitResult{ID: "config-ops", Name: "Config Ops", Version: "0.1.0", Description: "Schema-backed plugin settings", Protocol: subprocess.ProtocolVersion}, nil
}
func (*plugin) Load(context.Context) (subprocess.LoadResult, error) {
	return subprocess.LoadResult{}, nil
}
func (*plugin) Unload(context.Context) error { return nil }
func (p *plugin) Command(ctx context.Context, req subprocess.CommandRequest) (subprocess.CommandResult, error) {
	if req.Name == "config_schemas" {
		if p.adapter == nil {
			return subprocess.CommandResult{}, fmt.Errorf("config adapter is not initialized")
		}
		var targets []ConfigTarget
		if err := json.Unmarshal([]byte(req.Args), &targets); err != nil {
			return subprocess.CommandResult{}, fmt.Errorf("invalid config schemas")
		}
		if err := p.adapter.SetSchemas(targets); err != nil {
			return subprocess.CommandResult{}, err
		}
		encoded, err := json.Marshal(contract.ResultEnvelope{Status: contract.StatusOK})
		return subprocess.CommandResult{Action: "message", Content: string(encoded)}, err
	}
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

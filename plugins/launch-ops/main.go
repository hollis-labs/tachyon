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

type plugin struct {
	adapter LaunchAdapter
	caps    contract.PluginCapabilities
}

// Capabilities implements pluginkit.VerbPlugin. Returns the statically
// embedded PluginCapabilities parsed at Init time.
func (p *plugin) Capabilities() contract.PluginCapabilities {
	return p.caps
}

func (p *plugin) Init(_ context.Context, params subprocess.InitParams) (subprocess.InitResult, error) {
	// Parse embedded capabilities at startup so Capabilities() is
	// zero-alloc and Validate() runs once, not per call.
	if err := json.Unmarshal(capabilitiesJSON, &p.caps); err != nil {
		return subprocess.InitResult{}, fmt.Errorf("parse capabilities.json: %w", err)
	}
	if err := p.caps.Validate(); err != nil {
		return subprocess.InitResult{}, fmt.Errorf("validate capabilities: %w", err)
	}

	// Default Nanite API URL — overridable via plugin config.
	naniteURL := "http://localhost:8090"
	if url, ok := params.Config["nanite_url"]; ok {
		naniteURL = url
	}

	p.adapter = NewNaniteLaunchAdapter(naniteURL)

	return subprocess.InitResult{
		ID:          "launch-ops",
		Name:        "Launch Ops",
		Version:     "0.1.0",
		Description: "Two-phase agent execution orchestration for Tachyon",
		Protocol:    subprocess.ProtocolVersion,
	}, nil
}

func (p *plugin) Load(_ context.Context) (subprocess.LoadResult, error) {
	return subprocess.LoadResult{}, nil
}

func (p *plugin) Unload(_ context.Context) error {
	return nil
}

// Command dispatches capabilities and declared verbs through the host shim.
func (p *plugin) Command(ctx context.Context, req subprocess.CommandRequest) (subprocess.CommandResult, error) {
	result, handled, err := pluginkit.Dispatch(ctx, p, req)
	if err != nil || handled {
		return result, err
	}
	return subprocess.CommandResult{}, fmt.Errorf("unknown command: %s", req.Name)
}

func main() {
	if err := subprocess.Serve(&plugin{}); err != nil {
		os.Exit(1)
	}
}

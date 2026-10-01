// launch-ops is a subprocess plugin for Tachyon that provides two-phase
// agent execution orchestration: prepare intent, then execute.
//
// Verb dispatch rides command/execute through the pluginkit shim
// (CW-20261001-0469). The plugin implements pluginkit.VerbPlugin and
// calls pluginkit.Dispatch at the top of Command(). Until pluginkit
// lands on main, the plugin compiles and its verb handler is tested
// directly; the Dispatch integration is wired once the dependency
// merges.
package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"

	"github.com/hollis-labs/plugin-sdk/subprocess"
	"github.com/hollis-labs/tachyon/internal/contract"
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

// Command implements subprocess.CommandHandler. When the pluginkit shim
// lands (CW-20261001-0469), the first thing this method does is call
// pluginkit.Dispatch(ctx, p, req) which handles:
//   - "plugin_capabilities" → returns p.Capabilities() as JSON
//   - any declared verb name → calls p.HandleVerb and wraps the
//     ResultEnvelope as CommandResult.Content
//
// If Dispatch returns handled=false, we fall through to legacy commands.
//
// Until pluginkit merges, this method handles verbs directly via a
// local dispatch that mirrors the same contract: command name = verb,
// args = JSON payload, result content = JSON-encoded ResultEnvelope.
func (p *plugin) Command(ctx context.Context, req subprocess.CommandRequest) (subprocess.CommandResult, error) {
	// --- pluginkit shim dispatch (uncomment when CW-20261001-0469 merges) ---
	// result, handled, err := pluginkit.Dispatch(ctx, p, req)
	// if err != nil {
	// 	return subprocess.CommandResult{}, err
	// }
	// if handled {
	// 	return result, nil
	// }

	// --- interim verb dispatch until pluginkit lands ---
	if req.Name == "plugin_capabilities" {
		content, err := json.Marshal(p.caps)
		if err != nil {
			return subprocess.CommandResult{}, err
		}
		return subprocess.CommandResult{Action: "message", Content: string(content)}, nil
	}

	// Check if this is a declared verb
	if _, declared := p.caps.Verbs[req.Name]; declared {
		var payload json.RawMessage
		if req.Args != "" {
			payload = json.RawMessage(req.Args)
		}
		env, err := p.HandleVerb(ctx, req.Name, payload)
		if err != nil {
			return subprocess.CommandResult{}, err
		}
		content, err := json.Marshal(env)
		if err != nil {
			return subprocess.CommandResult{}, err
		}
		return subprocess.CommandResult{Action: "message", Content: string(content)}, nil
	}

	return subprocess.CommandResult{}, fmt.Errorf("unknown command: %s", req.Name)
}

func main() {
	if err := subprocess.Serve(&plugin{}); err != nil {
		os.Exit(1)
	}
}

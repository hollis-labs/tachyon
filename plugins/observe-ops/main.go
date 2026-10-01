// observe-ops is a subprocess plugin for Tachyon that provides read-only
// observability: activity feeds, structured logs, telemetry metrics,
// lifecycle events, aggregate health status, and subscription handles.
//
// It claims the "observe" module namespace and implements verb dispatch
// via the pluginkit shim (internal/pluginkit), which carries the ADR 001
// verb contract over the plugin-sdk v0.5.0 command/execute method.
//
// MVP uses a local in-memory event aggregator. Tether session telemetry
// and Flux activity integration are deferred.
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
	adapter ObserveAdapter
	caps    contract.PluginCapabilities
}

// --- pluginkit.VerbPlugin implementation ---

// Capabilities returns the embedded capability declaration for this plugin.
func (p *plugin) Capabilities() contract.PluginCapabilities {
	return p.caps
}

// HandleVerb dispatches a verb invocation to the appropriate adapter
// method. This is the pluginkit.VerbPlugin interface method.
func (p *plugin) HandleVerb(ctx context.Context, verb string, payload json.RawMessage) (contract.ResultEnvelope, error) {
	return p.handleVerb(ctx, verb, payload)
}

// --- subprocess.Plugin lifecycle ---

func (p *plugin) Init(_ context.Context, _ subprocess.InitParams) (subprocess.InitResult, error) {
	// Parse embedded capabilities.
	if err := json.Unmarshal(capabilitiesJSON, &p.caps); err != nil {
		return subprocess.InitResult{}, fmt.Errorf("parse embedded capabilities: %w", err)
	}
	if err := p.caps.Validate(); err != nil {
		return subprocess.InitResult{}, fmt.Errorf("validate capabilities: %w", err)
	}

	// Initialize the local adapter (MVP).
	p.adapter = NewLocalAdapter(1000)

	return subprocess.InitResult{
		ID:          "observe-ops",
		Name:        "Observe Ops",
		Version:     "0.1.0",
		Description: "Read-only observability for Tachyon — activity feeds, logs, metrics, events, status",
		Protocol:    subprocess.ProtocolVersion,
	}, nil
}

func (p *plugin) Load(_ context.Context) (subprocess.LoadResult, error) {
	return subprocess.LoadResult{}, nil
}

func (p *plugin) Unload(_ context.Context) error {
	return nil
}

// --- subprocess.CommandHandler ---

// Command dispatches command/execute calls. pluginkit.Dispatch handles
// verb invocations (including plugin_capabilities); unhandled commands
// fall through to legacy dispatch.
func (p *plugin) Command(ctx context.Context, req subprocess.CommandRequest) (subprocess.CommandResult, error) {
	result, handled, err := pluginkit.Dispatch(ctx, p, req)
	if handled || err != nil {
		return result, err
	}
	// No legacy commands in observe-ops.
	return subprocess.CommandResult{}, fmt.Errorf("unknown command: %s", req.Name)
}

func main() {
	if err := subprocess.Serve(&plugin{}); err != nil {
		os.Exit(1)
	}
}

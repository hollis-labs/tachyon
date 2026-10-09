// Package pluginkit carries Tachyon's verb contract over plugin-sdk commands.
package pluginkit

import (
	"context"
	"encoding/json"

	"github.com/hollis-labs/libs/plugin-mcp/plugin-sdk/subprocess"
	"github.com/hollis-labs/tachyon/internal/contract"
)

// CommandCapabilities is reserved for the plugin's host capability declaration.
const CommandCapabilities = "plugin_capabilities"

// VerbPlugin supplies a declaration and executes its declared verbs.
type VerbPlugin interface {
	Capabilities() contract.PluginCapabilities
	HandleVerb(ctx context.Context, verb string, payload json.RawMessage) (contract.ResultEnvelope, error)
}

// Dispatch handles capability discovery and declared verbs. Unhandled commands
// must fall through to the plugin's legacy command handler.
func Dispatch(ctx context.Context, p VerbPlugin, req subprocess.CommandRequest) (subprocess.CommandResult, bool, error) {
	caps := p.Capabilities()
	var value any
	if req.Name == CommandCapabilities {
		value = caps
	} else {
		if _, declared := caps.Verbs[req.Name]; !declared {
			return subprocess.CommandResult{}, false, nil
		}
		var payload json.RawMessage
		if req.Args != "" {
			payload = json.RawMessage(req.Args)
		}
		result, err := p.HandleVerb(ctx, req.Name, payload)
		if err != nil {
			return subprocess.CommandResult{}, true, err
		}
		value = result
	}
	content, err := json.Marshal(value)
	if err != nil {
		return subprocess.CommandResult{}, true, err
	}
	return subprocess.CommandResult{Action: "message", Content: string(content)}, true, nil
}

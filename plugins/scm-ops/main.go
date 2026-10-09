// scm-ops exposes local, read-only Git operations through Tachyon's verb shim.
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
)

//go:embed capabilities.json
var capabilitiesJSON []byte

type plugin struct {
	adapter SCMAdapter
	caps    contract.PluginCapabilities
}

var _ pluginkit.VerbPlugin = (*plugin)(nil)

func (p *plugin) Capabilities() contract.PluginCapabilities { return p.caps }

func (p *plugin) Init(_ context.Context, params subprocess.InitParams) (subprocess.InitResult, error) {
	if err := json.Unmarshal(capabilitiesJSON, &p.caps); err != nil {
		return subprocess.InitResult{}, err
	}
	if err := p.caps.Validate(); err != nil {
		return subprocess.InitResult{}, err
	}
	root := params.Config["repos_root"]
	// Preserve meaningful edge spaces in nonblank filesystem names.
	if strings.TrimSpace(root) == "" {
		root = os.Getenv("TACHYON_SCM_REPOS_ROOT")
	}
	if strings.TrimSpace(root) == "" {
		root = "~/dev"
	}
	adapter, err := NewGitAdapter(root)
	if err != nil {
		return subprocess.InitResult{}, err
	}
	p.adapter = adapter
	return subprocess.InitResult{ID: "scm-ops", Name: "Source Control", Version: "0.1.0", Description: "Read-only local Git repository activity and status", CapabilityContract: 1, Protocol: subprocess.ProtocolVersion}, nil
}

func (p *plugin) Load(context.Context) (subprocess.LoadResult, error) {
	return subprocess.LoadResult{}, nil
}
func (p *plugin) Unload(context.Context) error { return nil }

func (p *plugin) Command(ctx context.Context, req subprocess.CommandRequest) (subprocess.CommandResult, error) {
	result, handled, err := pluginkit.Dispatch(ctx, p, req)
	if handled || err != nil {
		return result, err
	}
	return subprocess.CommandResult{}, fmt.Errorf("unknown command: %s", req.Name)
}

func main() {
	if err := subprocess.Serve(&plugin{}); err != nil {
		os.Exit(1)
	}
}

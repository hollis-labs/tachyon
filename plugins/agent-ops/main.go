// Agent Ops is a subprocess plugin for Tachyon that provides agent
// operational capabilities including session management, task tracking,
// and agent lifecycle operations.
package main

import (
	"context"
	"os"

	"github.com/hollis-labs/plugin-sdk/subprocess"
)

type plugin struct{}

func (p *plugin) Init(ctx context.Context, params subprocess.InitParams) (subprocess.InitResult, error) {
	return subprocess.InitResult{
		ID:          "agent-ops",
		Name:        "Agent Ops",
		Version:     "0.1.0",
		Description: "Agent operational capabilities for Tachyon",
		Protocol:    subprocess.ProtocolVersion,
	}, nil
}

func (p *plugin) Load(ctx context.Context) (subprocess.LoadResult, error) {
	return subprocess.LoadResult{}, nil
}

func (p *plugin) Unload(ctx context.Context) error {
	return nil
}

func main() {
	if err := subprocess.Serve(&plugin{}); err != nil {
		os.Exit(1)
	}
}

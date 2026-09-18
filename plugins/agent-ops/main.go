// Agent Ops is a subprocess plugin for Tachyon that provides agent
// operational capabilities including session management, task tracking,
// and agent lifecycle operations.
//
// This plugin uses an adapter-based architecture to support multiple
// agent frameworks. The AgentAdapter interface defines provider-agnostic
// operations (list/get/create/update/delete/launch), and concrete adapters
// implement these operations for specific frameworks.
//
// The first concrete adapter is NaniteAdapter, which calls Nanite's HTTP
// API directly at http://localhost:8090 (the nanite-api-service daemon).
// Future adapters can plug in for other agent frameworks.
package main

import (
	"context"
	"os"

	"github.com/hollis-labs/plugin-sdk/subprocess"
)

type plugin struct {
	adapter AgentAdapter
}

func (p *plugin) Init(ctx context.Context, params subprocess.InitParams) (subprocess.InitResult, error) {
	// Default Nanite API URL - can be overridden via config
	naniteURL := "http://localhost:8090"
	if url, ok := params.Config["nanite_url"]; ok {
		naniteURL = url
	}

	// Initialize the Nanite adapter (first concrete implementation)
	p.adapter = NewNaniteAdapter(naniteURL)

	return subprocess.InitResult{
		ID:          "agent-ops",
		Name:        "Agent Ops",
		Version:     "0.1.0",
		Description: "Agent operational capabilities for Tachyon (adapter-based, Nanite HTTP API)",
		Protocol:    subprocess.ProtocolVersion,
	}, nil
}

func (p *plugin) Load(ctx context.Context) (subprocess.LoadResult, error) {
	// Plugin is ready - adapter initialized during Init
	return subprocess.LoadResult{}, nil
}

func (p *plugin) Unload(ctx context.Context) error {
	// Clean shutdown - adapter cleanup if needed
	return nil
}

func main() {
	if err := subprocess.Serve(&plugin{}); err != nil {
		os.Exit(1)
	}
}

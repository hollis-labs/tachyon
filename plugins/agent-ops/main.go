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
//
// CRUD is exposed over plugin-sdk's standard CRUDHandler wire contract
// (crud/create, crud/read, crud/update, crud/delete, crud/list) via
// subprocess.Serve, the same dispatch loop every other plugin in this
// portfolio uses — not a hand-rolled RPC loop.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/hollis-labs/plugin-sdk/subprocess"
)

const resourceTypeAgent = "agent"

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

// toMap round-trips a typed value through JSON into a generic map, the
// shape CRUDHandler works in.
func toMap(v interface{}) (map[string]interface{}, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var m map[string]interface{}
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	return m, nil
}

// fromMap round-trips a generic map back into a typed value.
func fromMap(m map[string]interface{}, dst interface{}) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, dst)
}

// Create implements subprocess.CRUDHandler.
func (p *plugin) Create(ctx context.Context, resourceType string, data map[string]interface{}) (map[string]interface{}, error) {
	if resourceType != resourceTypeAgent {
		return nil, fmt.Errorf("unknown resource type: %s", resourceType)
	}
	var req CreateAgentRequest
	if err := fromMap(data, &req); err != nil {
		return nil, fmt.Errorf("decode create request: %w", err)
	}
	agent, err := p.adapter.CreateAgent(ctx, req)
	if err != nil {
		return nil, err
	}
	return toMap(agent)
}

// Read implements subprocess.CRUDHandler.
func (p *plugin) Read(ctx context.Context, resourceType, id string) (map[string]interface{}, error) {
	if resourceType != resourceTypeAgent {
		return nil, fmt.Errorf("unknown resource type: %s", resourceType)
	}
	agent, err := p.adapter.GetAgent(ctx, id)
	if err != nil {
		return nil, err
	}
	return toMap(agent)
}

// Update implements subprocess.CRUDHandler.
func (p *plugin) Update(ctx context.Context, resourceType, id string, data map[string]interface{}) (map[string]interface{}, error) {
	if resourceType != resourceTypeAgent {
		return nil, fmt.Errorf("unknown resource type: %s", resourceType)
	}
	var req UpdateAgentRequest
	if err := fromMap(data, &req); err != nil {
		return nil, fmt.Errorf("decode update request: %w", err)
	}
	agent, err := p.adapter.UpdateAgent(ctx, id, req)
	if err != nil {
		return nil, err
	}
	return toMap(agent)
}

// Delete implements subprocess.CRUDHandler.
func (p *plugin) Delete(ctx context.Context, resourceType, id string) error {
	if resourceType != resourceTypeAgent {
		return fmt.Errorf("unknown resource type: %s", resourceType)
	}
	return p.adapter.DeleteAgent(ctx, id)
}

// List implements subprocess.CRUDHandler.
func (p *plugin) List(ctx context.Context, resourceType string, filters map[string]interface{}) ([]map[string]interface{}, error) {
	if resourceType != resourceTypeAgent {
		return nil, fmt.Errorf("unknown resource type: %s", resourceType)
	}
	agents, err := p.adapter.ListAgents(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]interface{}, 0, len(agents))
	for _, a := range agents {
		m, err := toMap(a)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, nil
}

// launchArgs is the JSON shape carried in CommandRequest.Args for the
// "launch" command (there is no standard CRUD verb for this).
type launchArgs struct {
	AgentID   string `json:"agent_id,omitempty"`
	ProjectID string `json:"project_id,omitempty"`
	Model     string `json:"model,omitempty"`
	Provider  string `json:"provider,omitempty"`
}

// Command implements subprocess.CommandHandler for the one operation that
// doesn't fit CRUD: launching a session for an agent.
func (p *plugin) Command(ctx context.Context, req subprocess.CommandRequest) (subprocess.CommandResult, error) {
	if req.Name != "launch" {
		return subprocess.CommandResult{}, fmt.Errorf("unknown command: %s", req.Name)
	}
	var args launchArgs
	if req.Args != "" {
		if err := json.Unmarshal([]byte(req.Args), &args); err != nil {
			return subprocess.CommandResult{}, fmt.Errorf("decode launch args: %w", err)
		}
	}
	sessionID, err := p.adapter.CreateSession(ctx, CreateSessionRequest{
		ProjectID: args.ProjectID,
		Model:     args.Model,
		Provider:  args.Provider,
		AgentID:   args.AgentID,
	})
	if err != nil {
		return subprocess.CommandResult{}, err
	}
	result, err := p.adapter.LaunchSession(ctx, sessionID)
	if err != nil {
		return subprocess.CommandResult{}, err
	}
	content, err := json.Marshal(result)
	if err != nil {
		return subprocess.CommandResult{}, err
	}
	return subprocess.CommandResult{Action: "message", Content: string(content)}, nil
}

func main() {
	if err := subprocess.Serve(&plugin{}); err != nil {
		os.Exit(1)
	}
}

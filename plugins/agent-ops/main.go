// Agent Ops is a subprocess plugin for Tachyon that provides agent
// operational capabilities including session management, task tracking,
// and agent lifecycle operations.
//
// This plugin uses an adapter-based architecture to support multiple
// agent frameworks. The AgentAdapter interface defines provider-agnostic
// operations (list/get/create/update/delete/launch plus tool/skill/MCP
// capability management and reflexes), and concrete adapters implement
// these operations for specific frameworks.
//
// The first concrete adapter is NaniteAdapter, which calls Nanite's HTTP
// API directly at http://localhost:8090 (the nanite-api-service daemon).
// Future adapters can plug in for other agent frameworks.
//
// CRUD is exposed over plugin-sdk's standard CRUDHandler wire contract
// (crud/create, crud/read, crud/update, crud/delete, crud/list) via
// subprocess.Serve, the same dispatch loop every other plugin in this
// portfolio uses — not a hand-rolled RPC loop. Agent-scoped sub-resources
// (a tool grant, a skill assignment, an MCP attachment, a reflex) reuse
// the same five verbs rather than inventing new Command actions:
//
//   - "list" resource types that are agent-scoped (agent-tool, agent-skill,
//     reflex) read the owning agent's ID out of the list filters
//     (`filters.agent_id`).
//   - "create" reads `data.agent_id` alongside the resource's own fields.
//   - "read"/"update"/"delete" address a compound identity the resource
//     can't express as a single opaque ID — e.g. one tool grant is really
//     (agent_id, tool_id) — as a "<agentID>::<subID>" compound ID (see
//     splitCompoundID). This keeps every sub-resource on the same wire
//     contract "agent" already uses instead of a second, parallel command
//     dispatch.
//
// "skill" and "mcp-server" are read-only catalog resource types (list
// only) — Tachyon manages grants/attachments against them, not the
// catalog entries themselves; skill authoring and MCP server registration
// stay on Nanite's own install/admin flows.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/hollis-labs/plugin-sdk/subprocess"
)

const (
	resourceTypeAgent           = "agent"
	resourceTypeAgentTool       = "agent-tool"
	resourceTypeSkill           = "skill"
	resourceTypeAgentSkill      = "agent-skill"
	resourceTypeAgentSkillGrant = "agent-skill-grant"
	resourceTypeMCPServer       = "mcp-server"
	resourceTypeAgentMCPServer  = "agent-mcp-server"
	resourceTypeReflex          = "reflex"

	resourceTypeDurableAgent        = "durable-agent"
	resourceTypeDurableAgentEvent   = "durable-agent-event"
	resourceTypeDurableAgentSession = "durable-agent-session"
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

// splitCompoundID splits a "<agentID>::<subID>" compound ID used to
// address an agent-scoped sub-resource (a tool grant, a skill assignment,
// an MCP attachment, a reflex) as a single opaque ID on the crud/read,
// crud/update, and crud/delete wire methods.
func splitCompoundID(id string) (agentID, subID string, err error) {
	parts := strings.SplitN(id, "::", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("expected a compound id of the form \"<agentID>::<subID>\", got %q", id)
	}
	return parts[0], parts[1], nil
}

func stringField(m map[string]interface{}, key string) (string, error) {
	v, ok := m[key]
	if !ok {
		return "", fmt.Errorf("missing required field: %s", key)
	}
	s, ok := v.(string)
	if !ok || s == "" {
		return "", fmt.Errorf("field %q must be a non-empty string", key)
	}
	return s, nil
}

// Create implements subprocess.CRUDHandler.
func (p *plugin) Create(ctx context.Context, resourceType string, data map[string]interface{}) (map[string]interface{}, error) {
	switch resourceType {
	case resourceTypeAgent:
		var req CreateAgentRequest
		if err := fromMap(data, &req); err != nil {
			return nil, fmt.Errorf("decode create request: %w", err)
		}
		agent, err := p.adapter.CreateAgent(ctx, req)
		if err != nil {
			return nil, err
		}
		return toMap(agent)

	case resourceTypeAgentTool:
		agentID, err := stringField(data, "agent_id")
		if err != nil {
			return nil, err
		}
		toolID, err := stringField(data, "tool_id")
		if err != nil {
			return nil, err
		}
		if err := p.adapter.GrantAgentTool(ctx, agentID, toolID); err != nil {
			return nil, err
		}
		return map[string]interface{}{"agent_id": agentID, "tool_id": toolID, "granted": true}, nil

	case resourceTypeAgentSkill:
		agentID, err := stringField(data, "agent_id")
		if err != nil {
			return nil, err
		}
		skillID, err := stringField(data, "skill_id")
		if err != nil {
			return nil, err
		}
		if err := p.adapter.AssignAgentSkill(ctx, agentID, skillID); err != nil {
			return nil, err
		}
		return map[string]interface{}{"agent_id": agentID, "skill_id": skillID, "assigned": true}, nil

	case resourceTypeAgentSkillGrant:
		agentID, err := stringField(data, "agent_id")
		if err != nil {
			return nil, err
		}
		skillSlug, err := stringField(data, "skill_slug")
		if err != nil {
			return nil, err
		}
		grantedBy, err := stringField(data, "granted_by")
		if err != nil {
			return nil, err
		}
		if err := p.adapter.GrantAgentSkill(ctx, agentID, skillSlug, grantedBy); err != nil {
			return nil, err
		}
		status, err := p.adapter.GetAgentSkillGrant(ctx, agentID, skillSlug)
		if err != nil {
			return nil, err
		}
		return toMap(status)

	case resourceTypeAgentMCPServer:
		agentID, err := stringField(data, "agent_id")
		if err != nil {
			return nil, err
		}
		serverName, err := stringField(data, "server_name")
		if err != nil {
			return nil, err
		}
		agent, err := p.adapter.AttachAgentMCPServer(ctx, agentID, serverName)
		if err != nil {
			return nil, err
		}
		return toMap(agent)

	case resourceTypeReflex:
		agentID, err := stringField(data, "agent_id")
		if err != nil {
			return nil, err
		}
		var req CreateReflexRequest
		if err := fromMap(data, &req); err != nil {
			return nil, fmt.Errorf("decode create request: %w", err)
		}
		reflex, err := p.adapter.CreateAgentReflex(ctx, agentID, req)
		if err != nil {
			return nil, err
		}
		return toMap(reflex)

	default:
		return nil, fmt.Errorf("unknown resource type: %s", resourceType)
	}
}

// Read implements subprocess.CRUDHandler.
func (p *plugin) Read(ctx context.Context, resourceType, id string) (map[string]interface{}, error) {
	switch resourceType {
	case resourceTypeAgent:
		agent, err := p.adapter.GetAgent(ctx, id)
		if err != nil {
			return nil, err
		}
		return toMap(agent)

	case resourceTypeAgentSkillGrant:
		agentID, skillSlug, err := splitCompoundID(id)
		if err != nil {
			return nil, err
		}
		status, err := p.adapter.GetAgentSkillGrant(ctx, agentID, skillSlug)
		if err != nil {
			return nil, err
		}
		return toMap(status)

	case resourceTypeDurableAgent:
		durableAgent, err := p.adapter.GetDurableAgent(ctx, id)
		if err != nil {
			return nil, err
		}
		return toMap(durableAgent)

	default:
		return nil, fmt.Errorf("unknown resource type: %s", resourceType)
	}
}

// Update implements subprocess.CRUDHandler.
func (p *plugin) Update(ctx context.Context, resourceType, id string, data map[string]interface{}) (map[string]interface{}, error) {
	switch resourceType {
	case resourceTypeAgent:
		var req UpdateAgentRequest
		if err := fromMap(data, &req); err != nil {
			return nil, fmt.Errorf("decode update request: %w", err)
		}
		agent, err := p.adapter.UpdateAgent(ctx, id, req)
		if err != nil {
			return nil, err
		}
		return toMap(agent)

	case resourceTypeReflex:
		agentID, reflexID, err := splitCompoundID(id)
		if err != nil {
			return nil, err
		}
		var req UpdateReflexRequest
		if err := fromMap(data, &req); err != nil {
			return nil, fmt.Errorf("decode update request: %w", err)
		}
		reflex, err := p.adapter.UpdateAgentReflex(ctx, agentID, reflexID, req)
		if err != nil {
			return nil, err
		}
		return toMap(reflex)

	default:
		return nil, fmt.Errorf("unknown resource type: %s", resourceType)
	}
}

// Delete implements subprocess.CRUDHandler.
func (p *plugin) Delete(ctx context.Context, resourceType, id string) error {
	switch resourceType {
	case resourceTypeAgent:
		return p.adapter.DeleteAgent(ctx, id)

	case resourceTypeAgentTool:
		agentID, toolID, err := splitCompoundID(id)
		if err != nil {
			return err
		}
		return p.adapter.RevokeAgentTool(ctx, agentID, toolID)

	case resourceTypeAgentSkill:
		agentID, skillID, err := splitCompoundID(id)
		if err != nil {
			return err
		}
		return p.adapter.RemoveAgentSkill(ctx, agentID, skillID)

	case resourceTypeAgentSkillGrant:
		agentID, skillSlug, err := splitCompoundID(id)
		if err != nil {
			return err
		}
		return p.adapter.RevokeAgentSkillGrant(ctx, agentID, skillSlug)

	case resourceTypeAgentMCPServer:
		agentID, serverName, err := splitCompoundID(id)
		if err != nil {
			return err
		}
		_, err = p.adapter.DetachAgentMCPServer(ctx, agentID, serverName)
		return err

	case resourceTypeReflex:
		agentID, reflexID, err := splitCompoundID(id)
		if err != nil {
			return err
		}
		return p.adapter.DeleteAgentReflex(ctx, agentID, reflexID)

	default:
		return fmt.Errorf("unknown resource type: %s", resourceType)
	}
}

// List implements subprocess.CRUDHandler.
func (p *plugin) List(ctx context.Context, resourceType string, filters map[string]interface{}) ([]map[string]interface{}, error) {
	switch resourceType {
	case resourceTypeAgent:
		agents, err := p.adapter.ListAgents(ctx)
		if err != nil {
			return nil, err
		}
		return mapSlice(agents)

	case resourceTypeAgentTool:
		agentID, err := stringField(filters, "agent_id")
		if err != nil {
			return nil, err
		}
		tools, err := p.adapter.ListAgentTools(ctx, agentID)
		if err != nil {
			return nil, err
		}
		return mapSlice(tools)

	case resourceTypeSkill:
		skills, err := p.adapter.ListSkillCatalog(ctx)
		if err != nil {
			return nil, err
		}
		return mapSlice(skills)

	case resourceTypeAgentSkill:
		agentID, err := stringField(filters, "agent_id")
		if err != nil {
			return nil, err
		}
		skills, err := p.adapter.ListAgentSkills(ctx, agentID)
		if err != nil {
			return nil, err
		}
		return mapSlice(skills)

	case resourceTypeMCPServer:
		servers, err := p.adapter.ListMCPServerCatalog(ctx)
		if err != nil {
			return nil, err
		}
		return mapSlice(servers)

	case resourceTypeReflex:
		agentID, err := stringField(filters, "agent_id")
		if err != nil {
			return nil, err
		}
		reflexes, err := p.adapter.ListAgentReflexes(ctx, agentID)
		if err != nil {
			return nil, err
		}
		return mapSlice(reflexes)

	case resourceTypeDurableAgent:
		durableAgents, err := p.adapter.ListDurableAgents(ctx)
		if err != nil {
			return nil, err
		}
		return mapSlice(durableAgents)

	case resourceTypeDurableAgentEvent:
		instanceID, err := stringField(filters, "instance_id")
		if err != nil {
			return nil, err
		}
		events, err := p.adapter.ListDurableAgentEvents(ctx, instanceID)
		if err != nil {
			return nil, err
		}
		return mapSlice(events)

	case resourceTypeDurableAgentSession:
		instanceID, err := stringField(filters, "instance_id")
		if err != nil {
			return nil, err
		}
		sessions, err := p.adapter.ListDurableAgentSessions(ctx, instanceID)
		if err != nil {
			return nil, err
		}
		return mapSlice(sessions)

	default:
		return nil, fmt.Errorf("unknown resource type: %s", resourceType)
	}
}

// mapSlice round-trips a typed slice through JSON into the generic
// []map[string]interface{} shape CRUDHandler.List works in.
func mapSlice[T any](items []T) ([]map[string]interface{}, error) {
	out := make([]map[string]interface{}, 0, len(items))
	for _, it := range items {
		m, err := toMap(it)
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
	switch req.Name {
	case "launch":
		return p.commandLaunch(ctx, req)
	case "capabilities":
		return p.commandCapabilities(ctx)
	default:
		return subprocess.CommandResult{}, fmt.Errorf("unknown command: %s", req.Name)
	}
}

func (p *plugin) commandLaunch(ctx context.Context, req subprocess.CommandRequest) (subprocess.CommandResult, error) {
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

// commandCapabilities implements the "capabilities" command: the
// provider-neutral capability declaration a consumer checks before
// offering an action (e.g. whether to show "New Agent" at all).
func (p *plugin) commandCapabilities(ctx context.Context) (subprocess.CommandResult, error) {
	caps, err := p.adapter.Capabilities(ctx)
	if err != nil {
		return subprocess.CommandResult{}, err
	}
	content, err := json.Marshal(caps)
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

package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hollis-labs/tachyon/internal/contract"
)

// handleVerb dispatches a verb/invoke call to the appropriate adapter
// method and returns a ResultEnvelope. This is the verb-based dispatch
// layer that will eventually replace the CRUD/command dispatch.
func (p *plugin) handleVerb(ctx context.Context, verb string, payload json.RawMessage) (contract.ResultEnvelope, error) {
	switch verb {
	// --- CRUD verbs ---
	case "agent_create":
		return p.verbAgentCreate(ctx, payload)
	case "agent_read":
		return p.verbAgentRead(ctx, payload)
	case "agent_update":
		return p.verbAgentUpdate(ctx, payload)
	case "agent_delete":
		return p.verbAgentDelete(ctx, payload)
	case "agent_list":
		return p.verbAgentList(ctx, payload)

	// --- Grant verbs ---
	case "agent_grant":
		return p.verbAgentGrant(ctx, payload)
	case "agent_revoke":
		return p.verbAgentRevoke(ctx, payload)
	case "agent_grants":
		return p.verbAgentGrants(ctx, payload)

	// --- Capabilities ---
	case "agent_capabilities":
		return p.verbAgentCapabilities(ctx)

	// --- Reflex verbs ---
	case "agent_reflex_create":
		return p.verbAgentReflexCreate(ctx, payload)
	case "agent_reflex_update":
		return p.verbAgentReflexUpdate(ctx, payload)
	case "agent_reflex_delete":
		return p.verbAgentReflexDelete(ctx, payload)
	case "agent_reflex_list":
		return p.verbAgentReflexList(ctx, payload)

	// --- Approval ---
	case "agent_approve":
		return p.verbAgentApprove(ctx, payload)

	// --- Launch ---
	case "agent_launch":
		return p.verbAgentLaunch(ctx, payload)

	default:
		return contract.Err("unknown_verb", fmt.Sprintf("verb %q is not implemented", verb)), nil
	}
}

func (p *plugin) verbAgentCreate(ctx context.Context, payload json.RawMessage) (contract.ResultEnvelope, error) {
	var req CreateAgentRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		return contract.Err("validation", "invalid create request: "+err.Error()), nil
	}
	agent, err := p.adapter.CreateAgent(ctx, req)
	if err != nil {
		return contract.Err("provider_error", err.Error()), nil
	}
	return contract.OK(agent)
}

func (p *plugin) verbAgentRead(ctx context.Context, payload json.RawMessage) (contract.ResultEnvelope, error) {
	var params struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(payload, &params); err != nil || params.ID == "" {
		return contract.Err("validation", "id is required"), nil
	}
	agent, err := p.adapter.GetAgent(ctx, params.ID)
	if err != nil {
		return contract.Err("provider_error", err.Error()), nil
	}
	return contract.OK(agent)
}

func (p *plugin) verbAgentUpdate(ctx context.Context, payload json.RawMessage) (contract.ResultEnvelope, error) {
	var params struct {
		ID string `json:"id"`
		UpdateAgentRequest
	}
	if err := json.Unmarshal(payload, &params); err != nil || params.ID == "" {
		return contract.Err("validation", "id is required"), nil
	}
	agent, err := p.adapter.UpdateAgent(ctx, params.ID, params.UpdateAgentRequest)
	if err != nil {
		return contract.Err("provider_error", err.Error()), nil
	}
	return contract.OK(agent)
}

func (p *plugin) verbAgentDelete(ctx context.Context, payload json.RawMessage) (contract.ResultEnvelope, error) {
	var params struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(payload, &params); err != nil || params.ID == "" {
		return contract.Err("validation", "id is required"), nil
	}
	if err := p.adapter.DeleteAgent(ctx, params.ID); err != nil {
		return contract.Err("provider_error", err.Error()), nil
	}
	return contract.OK(map[string]bool{"deleted": true})
}

func (p *plugin) verbAgentList(ctx context.Context, payload json.RawMessage) (contract.ResultEnvelope, error) {
	agents, err := p.adapter.ListAgents(ctx)
	if err != nil {
		return contract.Err("provider_error", err.Error()), nil
	}
	return contract.OK(agents)
}

func (p *plugin) verbAgentGrant(ctx context.Context, payload json.RawMessage) (contract.ResultEnvelope, error) {
	var params struct {
		AgentID   string `json:"agent_id"`
		GrantType string `json:"grant_type"` // "tool", "skill", "mcp_server"
		TargetID  string `json:"target_id"`
		GrantedBy string `json:"granted_by,omitempty"` // for skill grants
	}
	if err := json.Unmarshal(payload, &params); err != nil {
		return contract.Err("validation", "invalid grant request: "+err.Error()), nil
	}
	if params.AgentID == "" || params.GrantType == "" || params.TargetID == "" {
		return contract.Err("validation", "agent_id, grant_type, and target_id are required"), nil
	}

	switch params.GrantType {
	case "tool":
		if err := p.adapter.GrantAgentTool(ctx, params.AgentID, params.TargetID); err != nil {
			return contract.Err("provider_error", err.Error()), nil
		}
		return contract.OK(map[string]any{"agent_id": params.AgentID, "grant_type": "tool", "target_id": params.TargetID, "granted": true})
	case "skill":
		if err := p.adapter.AssignAgentSkill(ctx, params.AgentID, params.TargetID); err != nil {
			return contract.Err("provider_error", err.Error()), nil
		}
		return contract.OK(map[string]any{"agent_id": params.AgentID, "grant_type": "skill", "target_id": params.TargetID, "granted": true})
	case "mcp_server":
		agent, err := p.adapter.AttachAgentMCPServer(ctx, params.AgentID, params.TargetID)
		if err != nil {
			return contract.Err("provider_error", err.Error()), nil
		}
		return contract.OK(agent)
	default:
		return contract.Err("validation", fmt.Sprintf("unknown grant_type: %s", params.GrantType)), nil
	}
}

func (p *plugin) verbAgentRevoke(ctx context.Context, payload json.RawMessage) (contract.ResultEnvelope, error) {
	var params struct {
		AgentID   string `json:"agent_id"`
		GrantType string `json:"grant_type"`
		TargetID  string `json:"target_id"`
	}
	if err := json.Unmarshal(payload, &params); err != nil {
		return contract.Err("validation", "invalid revoke request: "+err.Error()), nil
	}
	if params.AgentID == "" || params.GrantType == "" || params.TargetID == "" {
		return contract.Err("validation", "agent_id, grant_type, and target_id are required"), nil
	}

	switch params.GrantType {
	case "tool":
		if err := p.adapter.RevokeAgentTool(ctx, params.AgentID, params.TargetID); err != nil {
			return contract.Err("provider_error", err.Error()), nil
		}
	case "skill":
		if err := p.adapter.RemoveAgentSkill(ctx, params.AgentID, params.TargetID); err != nil {
			return contract.Err("provider_error", err.Error()), nil
		}
	case "mcp_server":
		if _, err := p.adapter.DetachAgentMCPServer(ctx, params.AgentID, params.TargetID); err != nil {
			return contract.Err("provider_error", err.Error()), nil
		}
	default:
		return contract.Err("validation", fmt.Sprintf("unknown grant_type: %s", params.GrantType)), nil
	}
	return contract.OK(map[string]any{"agent_id": params.AgentID, "grant_type": params.GrantType, "target_id": params.TargetID, "revoked": true})
}

func (p *plugin) verbAgentGrants(ctx context.Context, payload json.RawMessage) (contract.ResultEnvelope, error) {
	var params struct {
		AgentID   string `json:"agent_id"`
		GrantType string `json:"grant_type,omitempty"` // optional filter
	}
	if err := json.Unmarshal(payload, &params); err != nil || params.AgentID == "" {
		return contract.Err("validation", "agent_id is required"), nil
	}

	result := make(map[string]any)

	if params.GrantType == "" || params.GrantType == "tool" {
		tools, err := p.adapter.ListAgentTools(ctx, params.AgentID)
		if err != nil {
			return contract.Err("provider_error", err.Error()), nil
		}
		result["tools"] = tools
	}

	if params.GrantType == "" || params.GrantType == "skill" {
		skills, err := p.adapter.ListAgentSkills(ctx, params.AgentID)
		if err != nil {
			return contract.Err("provider_error", err.Error()), nil
		}
		result["skills"] = skills
	}

	return contract.OK(result)
}

func (p *plugin) verbAgentCapabilities(ctx context.Context) (contract.ResultEnvelope, error) {
	caps, err := p.adapter.Capabilities(ctx)
	if err != nil {
		return contract.Err("provider_error", err.Error()), nil
	}
	return contract.OK(caps)
}

func (p *plugin) verbAgentReflexCreate(ctx context.Context, payload json.RawMessage) (contract.ResultEnvelope, error) {
	var params struct {
		AgentID string `json:"agent_id"`
		CreateReflexRequest
	}
	if err := json.Unmarshal(payload, &params); err != nil || params.AgentID == "" {
		return contract.Err("validation", "agent_id is required"), nil
	}
	reflex, err := p.adapter.CreateAgentReflex(ctx, params.AgentID, params.CreateReflexRequest)
	if err != nil {
		return contract.Err("provider_error", err.Error()), nil
	}
	return contract.OK(reflex)
}

func (p *plugin) verbAgentReflexUpdate(ctx context.Context, payload json.RawMessage) (contract.ResultEnvelope, error) {
	var params struct {
		AgentID  string `json:"agent_id"`
		ReflexID string `json:"reflex_id"`
		UpdateReflexRequest
	}
	if err := json.Unmarshal(payload, &params); err != nil {
		return contract.Err("validation", "invalid request: "+err.Error()), nil
	}
	if params.AgentID == "" || params.ReflexID == "" {
		return contract.Err("validation", "agent_id and reflex_id are required"), nil
	}
	reflex, err := p.adapter.UpdateAgentReflex(ctx, params.AgentID, params.ReflexID, params.UpdateReflexRequest)
	if err != nil {
		return contract.Err("provider_error", err.Error()), nil
	}
	return contract.OK(reflex)
}

func (p *plugin) verbAgentReflexDelete(ctx context.Context, payload json.RawMessage) (contract.ResultEnvelope, error) {
	var params struct {
		AgentID  string `json:"agent_id"`
		ReflexID string `json:"reflex_id"`
	}
	if err := json.Unmarshal(payload, &params); err != nil {
		return contract.Err("validation", "invalid request: "+err.Error()), nil
	}
	if params.AgentID == "" || params.ReflexID == "" {
		return contract.Err("validation", "agent_id and reflex_id are required"), nil
	}
	if err := p.adapter.DeleteAgentReflex(ctx, params.AgentID, params.ReflexID); err != nil {
		return contract.Err("provider_error", err.Error()), nil
	}
	return contract.OK(map[string]bool{"deleted": true})
}

func (p *plugin) verbAgentReflexList(ctx context.Context, payload json.RawMessage) (contract.ResultEnvelope, error) {
	var params struct {
		AgentID string `json:"agent_id"`
	}
	if err := json.Unmarshal(payload, &params); err != nil || params.AgentID == "" {
		return contract.Err("validation", "agent_id is required"), nil
	}
	reflexes, err := p.adapter.ListAgentReflexes(ctx, params.AgentID)
	if err != nil {
		return contract.Err("provider_error", err.Error()), nil
	}
	return contract.OK(reflexes)
}

func (p *plugin) verbAgentApprove(ctx context.Context, payload json.RawMessage) (contract.ResultEnvelope, error) {
	var params struct {
		AgentID   string `json:"agent_id"`
		SkillSlug string `json:"skill_slug"`
		GrantedBy string `json:"granted_by"`
	}
	if err := json.Unmarshal(payload, &params); err != nil {
		return contract.Err("validation", "invalid request: "+err.Error()), nil
	}
	if params.AgentID == "" || params.SkillSlug == "" || params.GrantedBy == "" {
		return contract.Err("validation", "agent_id, skill_slug, and granted_by are required"), nil
	}
	if err := p.adapter.GrantAgentSkill(ctx, params.AgentID, params.SkillSlug, params.GrantedBy); err != nil {
		return contract.Err("provider_error", err.Error()), nil
	}
	status, err := p.adapter.GetAgentSkillGrant(ctx, params.AgentID, params.SkillSlug)
	if err != nil {
		return contract.Err("provider_error", err.Error()), nil
	}
	return contract.OK(status)
}

func (p *plugin) verbAgentLaunch(ctx context.Context, payload json.RawMessage) (contract.ResultEnvelope, error) {
	var params struct {
		AgentID   string `json:"agent_id"`
		ProjectID string `json:"project_id,omitempty"`
		Model     string `json:"model,omitempty"`
		Provider  string `json:"provider,omitempty"`
	}
	if err := json.Unmarshal(payload, &params); err != nil || params.AgentID == "" {
		return contract.Err("validation", "agent_id is required"), nil
	}
	sessionID, err := p.adapter.CreateSession(ctx, CreateSessionRequest{
		ProjectID: params.ProjectID,
		Model:     params.Model,
		Provider:  params.Provider,
		AgentID:   params.AgentID,
	})
	if err != nil {
		return contract.Err("provider_error", err.Error()), nil
	}
	result, err := p.adapter.LaunchSession(ctx, sessionID)
	if err != nil {
		return contract.Err("provider_error", err.Error()), nil
	}
	return contract.OK(result)
}

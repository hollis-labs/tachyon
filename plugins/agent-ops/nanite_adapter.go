package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// NaniteAdapter implements AgentAdapter by calling Nanite's HTTP API directly
// at http://localhost:8090. This targets Nanite's own daemon, not Tether/mux.
type NaniteAdapter struct {
	baseURL    string       // e.g., "http://localhost:8090"
	httpClient *http.Client
}

// NewNaniteAdapter creates a new Nanite adapter that calls the Nanite API
// at the given base URL (typically http://localhost:8090).
func NewNaniteAdapter(baseURL string) *NaniteAdapter {
	return &NaniteAdapter{
		baseURL:    baseURL,
		httpClient: &http.Client{},
	}
}

// Capabilities implements AgentAdapter.Capabilities. Nanite supports the
// full agent-authoring surface today — every flag here is backed by a real
// endpoint this adapter already calls elsewhere in this file, not aspirational.
func (a *NaniteAdapter) Capabilities(ctx context.Context) (AgentCapabilities, error) {
	return AgentCapabilities{
		Provider:            "nanite",
		CanCreate:           true,
		CanUpdate:           true,
		CanDelete:           true,
		CanGrantTools:       true,
		CanAssignSkills:     true,
		CanAttachMCPServers: true,
		CanManageReflexes:   true,
	}, nil
}

// naniteAgent represents the Nanite API's agent response structure.
// This matches the AgentProfileView type from internal/api/types.go.
type naniteAgent struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Slug         string `json:"slug"`
	SystemPrompt string `json:"system_prompt"`
	Description  string `json:"description"`
	Tags         string `json:"tags"`
	Icon         string `json:"icon"`
	Status       string `json:"status"`
	Source       string `json:"source"`
	ManageClass  string `json:"manage_class"`
	Editable     bool   `json:"editable"`
	CanExecute   bool   `json:"can_execute"`
	MCPServers   string `json:"mcp_servers"`
	RoleTools    string `json:"role_tools"`
	RoleSkills   string `json:"role_skills"`
}

// naniteCreateAgentRequest matches Nanite's CreateAgentRequest type.
type naniteCreateAgentRequest struct {
	ID           string `json:"id,omitempty"`
	Name         string `json:"name"`
	Slug         string `json:"slug"`
	SystemPrompt string `json:"system_prompt"`
	Description  string `json:"description,omitempty"`
	Avatar       string `json:"avatar,omitempty"`
	Icon         string `json:"icon,omitempty"`
	Tags         string `json:"tags,omitempty"`
	Status       string `json:"status,omitempty"`
	Source       string `json:"source,omitempty"` // Defaults to "user" server-side if omitted
	CanExecute   bool   `json:"can_execute,omitempty"`
	MCPServers   string `json:"mcp_servers,omitempty"`
	RoleTools    string `json:"role_tools,omitempty"`
	RoleSkills   string `json:"role_skills,omitempty"`
}

// naniteUpdateAgentRequest matches Nanite's UpdateAgentRequest type.
type naniteUpdateAgentRequest struct {
	Name         *string `json:"name,omitempty"`
	Slug         *string `json:"slug,omitempty"`
	SystemPrompt *string `json:"system_prompt,omitempty"`
	Description  *string `json:"description,omitempty"`
	Icon         *string `json:"icon,omitempty"`
	Tags         *string `json:"tags,omitempty"`
	Status       *string `json:"status,omitempty"`
	CanExecute   *bool   `json:"can_execute,omitempty"`
	MCPServers   *string `json:"mcp_servers,omitempty"`
	RoleTools    *string `json:"role_tools,omitempty"`
	RoleSkills   *string `json:"role_skills,omitempty"`
}

// naniteCreateSessionRequest matches Nanite's CreateSessionRequest type.
type naniteCreateSessionRequest struct {
	ProjectID string `json:"project_id,omitempty"`
	Model     string `json:"model,omitempty"`
	Provider  string `json:"provider,omitempty"`
	AgentID   string `json:"agent_id,omitempty"`
}

// naniteSession represents Nanite's session response.
type naniteSession struct {
	ID        string `json:"id"`
	ProjectID string `json:"project_id,omitempty"`
	Model     string `json:"model,omitempty"`
	Provider  string `json:"provider,omitempty"`
}

// doJSON is the shared request/decode helper every method below uses:
// build a JSON request (or none, for GET/DELETE), send it, check the
// status against wantStatus, and decode the response body into out (when
// out is non-nil).
func (a *NaniteAdapter) doJSON(ctx context.Context, method, path string, body interface{}, out interface{}, wantStatus ...int) error {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("failed to marshal request: %w", err)
		}
		reader = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, a.baseURL+path, reader)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s failed: %w", method, path, err)
	}
	defer resp.Body.Close()

	ok := len(wantStatus) == 0
	for _, s := range wantStatus {
		if resp.StatusCode == s {
			ok = true
			break
		}
	}
	if !ok {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("%s %s returned %d: %s", method, path, resp.StatusCode, string(respBody))
	}

	if out == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("failed to decode response: %w", err)
	}
	return nil
}

func toAgent(na naniteAgent) *Agent {
	return &Agent{
		ID:           na.ID,
		Name:         na.Name,
		Slug:         na.Slug,
		SystemPrompt: na.SystemPrompt,
		Description:  na.Description,
		Tags:         na.Tags,
		Icon:         na.Icon,
		Status:       na.Status,
		Layer:        na.ManageClass,
		Editable:     na.Editable,
		CanExecute:   na.CanExecute,
		MCPServers:   na.MCPServers,
		RoleTools:    na.RoleTools,
		RoleSkills:   na.RoleSkills,
	}
}

// ListAgents implements AgentAdapter.ListAgents.
func (a *NaniteAdapter) ListAgents(ctx context.Context) ([]Agent, error) {
	// Use ?manageable=1 to exclude internal harness primitives
	var naniteAgents []naniteAgent
	if err := a.doJSON(ctx, http.MethodGet, "/api/agents?manageable=1", nil, &naniteAgents, http.StatusOK); err != nil {
		return nil, err
	}

	agents := make([]Agent, len(naniteAgents))
	for i, na := range naniteAgents {
		agents[i] = *toAgent(na)
	}
	return agents, nil
}

// GetAgent implements AgentAdapter.GetAgent.
func (a *NaniteAdapter) GetAgent(ctx context.Context, id string) (*Agent, error) {
	// The GET endpoint wraps the agent in {"agent": ...}
	var wrapper struct {
		Agent naniteAgent `json:"agent"`
	}
	if err := a.doJSON(ctx, http.MethodGet, "/api/agents/"+id, nil, &wrapper, http.StatusOK); err != nil {
		return nil, err
	}
	return toAgent(wrapper.Agent), nil
}

// CreateAgent implements AgentAdapter.CreateAgent.
func (a *NaniteAdapter) CreateAgent(ctx context.Context, req CreateAgentRequest) (*Agent, error) {
	// Generate slug from ID if provided, otherwise from name
	slug := req.ID
	if slug == "" {
		// Convert name to slug format (lowercase, replace spaces with hyphens)
		slug = slugify(req.Name)
	}

	naniteReq := naniteCreateAgentRequest{
		Name:         req.Name,
		Slug:         slug,
		SystemPrompt: req.SystemPrompt,
		Description:  req.Description,
		CanExecute:   req.CanExecute,
		MCPServers:   req.MCPServers,
		RoleTools:    req.RoleTools,
		RoleSkills:   req.RoleSkills,
	}

	var na naniteAgent
	if err := a.doJSON(ctx, http.MethodPost, "/api/agents", naniteReq, &na, http.StatusOK, http.StatusCreated); err != nil {
		return nil, err
	}
	return toAgent(na), nil
}

// UpdateAgent implements AgentAdapter.UpdateAgent.
func (a *NaniteAdapter) UpdateAgent(ctx context.Context, id string, req UpdateAgentRequest) (*Agent, error) {
	naniteReq := naniteUpdateAgentRequest{
		Name:         req.Name,
		SystemPrompt: req.SystemPrompt,
		CanExecute:   req.CanExecute,
		MCPServers:   req.MCPServers,
		RoleTools:    req.RoleTools,
		RoleSkills:   req.RoleSkills,
	}
	if req.Description != nil {
		naniteReq.Description = req.Description
	}

	if err := a.doJSON(ctx, http.MethodPut, "/api/agents/"+id, naniteReq, nil, http.StatusOK); err != nil {
		return nil, err
	}

	// After update, fetch the current agent details
	return a.GetAgent(ctx, id)
}

// DeleteAgent implements AgentAdapter.DeleteAgent.
func (a *NaniteAdapter) DeleteAgent(ctx context.Context, id string) error {
	return a.doJSON(ctx, http.MethodDelete, "/api/agents/"+id, nil, nil, http.StatusOK, http.StatusNoContent)
}

// CreateSession implements AgentAdapter.CreateSession.
// This creates a Nanite session via POST /api/sessions.
func (a *NaniteAdapter) CreateSession(ctx context.Context, req CreateSessionRequest) (string, error) {
	naniteReq := naniteCreateSessionRequest{
		ProjectID: req.ProjectID,
		Model:     req.Model,
		Provider:  req.Provider,
		AgentID:   req.AgentID,
	}

	var session naniteSession
	if err := a.doJSON(ctx, http.MethodPost, "/api/sessions", naniteReq, &session, http.StatusOK, http.StatusCreated); err != nil {
		return "", err
	}
	if session.ID == "" {
		return "", fmt.Errorf("session ID not found in response")
	}
	return session.ID, nil
}

// LaunchSession implements AgentAdapter.LaunchSession.
// Note: Nanite sessions are created and immediately usable; there's no separate
// "launch" step in the API. We return the session ID in the result for consistency.
func (a *NaniteAdapter) LaunchSession(ctx context.Context, sessionID string) (*LaunchResult, error) {
	return &LaunchResult{
		SessionID: sessionID,
	}, nil
}

// naniteToolItem matches the per-tool shape GET /api/agents/{id}/tools
// returns (internal/api/tools.go's handleListAgentTools) — the full
// discoverable catalog with this agent's grant state folded in.
type naniteToolItem struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Allowed     bool   `json:"allowed"`
}

// ListAgentTools implements AgentAdapter.ListAgentTools.
func (a *NaniteAdapter) ListAgentTools(ctx context.Context, agentID string) ([]Tool, error) {
	var items []naniteToolItem
	if err := a.doJSON(ctx, http.MethodGet, "/api/agents/"+agentID+"/tools", nil, &items, http.StatusOK); err != nil {
		return nil, err
	}
	tools := make([]Tool, len(items))
	for i, it := range items {
		tools[i] = Tool{ID: it.ID, Name: it.Name, Description: it.Description, Granted: it.Allowed}
	}
	return tools, nil
}

// GrantAgentTool implements AgentAdapter.GrantAgentTool.
// POST /api/agents/{id}/tools {"tool_id": ...}
func (a *NaniteAdapter) GrantAgentTool(ctx context.Context, agentID, toolID string) error {
	body := map[string]string{"tool_id": toolID, "granted_via": "explicit"}
	return a.doJSON(ctx, http.MethodPost, "/api/agents/"+agentID+"/tools", body, nil, http.StatusOK, http.StatusCreated)
}

// RevokeAgentTool implements AgentAdapter.RevokeAgentTool.
// DELETE /api/agents/{id}/tools/{toolId}
func (a *NaniteAdapter) RevokeAgentTool(ctx context.Context, agentID, toolID string) error {
	return a.doJSON(ctx, http.MethodDelete, "/api/agents/"+agentID+"/tools/"+toolID, nil, nil, http.StatusOK, http.StatusNoContent)
}

// naniteSkill matches store.Skill's wire shape (internal/store/skills.go).
type naniteSkill struct {
	ID          string `json:"id"`
	Slug        string `json:"slug"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Category    string `json:"category"`
	ContentHash string `json:"content_hash"`
}

// ListSkillCatalog implements AgentAdapter.ListSkillCatalog.
// GET /api/skills
func (a *NaniteAdapter) ListSkillCatalog(ctx context.Context) ([]Skill, error) {
	var skills []naniteSkill
	if err := a.doJSON(ctx, http.MethodGet, "/api/skills", nil, &skills, http.StatusOK); err != nil {
		return nil, err
	}
	out := make([]Skill, len(skills))
	for i, s := range skills {
		out[i] = Skill{ID: s.ID, Slug: s.Slug, Name: s.Name, Description: s.Description, Category: s.Category, ContentHash: s.ContentHash}
	}
	return out, nil
}

// ListAgentSkills implements AgentAdapter.ListAgentSkills.
// GET /api/agents/{id}/skills returns the full skill catalog entry (not
// an agent_known_skills grant row — see GetAgentSkillGrant for approval
// state) for every skill currently assigned to this agent.
func (a *NaniteAdapter) ListAgentSkills(ctx context.Context, agentID string) ([]AgentSkill, error) {
	var rows []naniteSkill
	if err := a.doJSON(ctx, http.MethodGet, "/api/agents/"+agentID+"/skills", nil, &rows, http.StatusOK); err != nil {
		return nil, err
	}
	out := make([]AgentSkill, len(rows))
	for i, r := range rows {
		out[i] = AgentSkill{SkillSlug: r.Slug, Name: r.Name, Description: r.Description}
	}
	return out, nil
}

// AssignAgentSkill implements AgentAdapter.AssignAgentSkill.
// POST /api/agents/{id}/skills {"skill_id": ...}
func (a *NaniteAdapter) AssignAgentSkill(ctx context.Context, agentID, skillID string) error {
	body := map[string]string{"skill_id": skillID}
	return a.doJSON(ctx, http.MethodPost, "/api/agents/"+agentID+"/skills", body, nil, http.StatusOK, http.StatusCreated)
}

// RemoveAgentSkill implements AgentAdapter.RemoveAgentSkill.
// DELETE /api/agents/{id}/skills/{skillId}
func (a *NaniteAdapter) RemoveAgentSkill(ctx context.Context, agentID, skillID string) error {
	return a.doJSON(ctx, http.MethodDelete, "/api/agents/"+agentID+"/skills/"+skillID, nil, nil, http.StatusOK, http.StatusNoContent)
}

// naniteAgentSkillGrantView matches AgentSkillGrantView
// (internal/api/skills.go's handleGetAgentSkillGrant).
type naniteAgentSkillGrantView struct {
	AgentID             string `json:"agent_id"`
	SkillSlug           string `json:"skill_slug"`
	CurrentContentHash  string `json:"current_content_hash"`
	ApprovedContentHash string `json:"approved_content_hash"`
	GrantedAt           string `json:"granted_at"`
	GrantedBy           string `json:"granted_by"`
	Status              string `json:"status"`
	Message             string `json:"message"`
}

// GetAgentSkillGrant implements AgentAdapter.GetAgentSkillGrant.
// GET /api/agents/{id}/skills/{slug}/grant
func (a *NaniteAdapter) GetAgentSkillGrant(ctx context.Context, agentID, skillSlug string) (*SkillGrantStatus, error) {
	var view naniteAgentSkillGrantView
	if err := a.doJSON(ctx, http.MethodGet, "/api/agents/"+agentID+"/skills/"+skillSlug+"/grant", nil, &view, http.StatusOK); err != nil {
		return nil, err
	}
	return &SkillGrantStatus{
		AgentID:             view.AgentID,
		SkillSlug:           view.SkillSlug,
		CurrentContentHash:  view.CurrentContentHash,
		ApprovedContentHash: view.ApprovedContentHash,
		GrantedAt:           view.GrantedAt,
		GrantedBy:           view.GrantedBy,
		Status:              view.Status,
		Message:             view.Message,
	}, nil
}

// GrantAgentSkill implements AgentAdapter.GrantAgentSkill.
// POST /api/agents/{id}/skills/{slug}/grant {"granted_by": ...}
func (a *NaniteAdapter) GrantAgentSkill(ctx context.Context, agentID, skillSlug, grantedBy string) error {
	body := map[string]string{"granted_by": grantedBy}
	return a.doJSON(ctx, http.MethodPost, "/api/agents/"+agentID+"/skills/"+skillSlug+"/grant", body, nil, http.StatusOK, http.StatusCreated)
}

// RevokeAgentSkillGrant implements AgentAdapter.RevokeAgentSkillGrant.
// DELETE /api/agents/{id}/skills/{slug}/grant
func (a *NaniteAdapter) RevokeAgentSkillGrant(ctx context.Context, agentID, skillSlug string) error {
	return a.doJSON(ctx, http.MethodDelete, "/api/agents/"+agentID+"/skills/"+skillSlug+"/grant", nil, nil, http.StatusOK, http.StatusNoContent)
}

// naniteMCPServer is the subset of store.MCPServerConfig's wire shape this
// adapter cares about for an attach picker.
type naniteMCPServer struct {
	Name          string `json:"name"`
	TransportType string `json:"transport_type"`
}

// ListMCPServerCatalog implements AgentAdapter.ListMCPServerCatalog.
// GET /api/mcp-servers
func (a *NaniteAdapter) ListMCPServerCatalog(ctx context.Context) ([]MCPServer, error) {
	var servers []naniteMCPServer
	if err := a.doJSON(ctx, http.MethodGet, "/api/mcp-servers", nil, &servers, http.StatusOK); err != nil {
		return nil, err
	}
	out := make([]MCPServer, len(servers))
	for i, s := range servers {
		out[i] = MCPServer{Name: s.Name, TransportType: s.TransportType}
	}
	return out, nil
}

// AttachAgentMCPServer implements AgentAdapter.AttachAgentMCPServer.
// Nanite has no dedicated per-agent attach endpoint — mcp_servers is a
// plain JSON-array-string field on the agent row (see
// docs/adding-an-agent.md), so attach/detach means read-modify-write
// through PUT /api/agents/{id}.
func (a *NaniteAdapter) AttachAgentMCPServer(ctx context.Context, agentID, serverName string) (*Agent, error) {
	return a.updateAgentMCPServers(ctx, agentID, func(names []string) []string {
		for _, n := range names {
			if n == serverName {
				return names
			}
		}
		return append(names, serverName)
	})
}

// DetachAgentMCPServer implements AgentAdapter.DetachAgentMCPServer.
func (a *NaniteAdapter) DetachAgentMCPServer(ctx context.Context, agentID, serverName string) (*Agent, error) {
	return a.updateAgentMCPServers(ctx, agentID, func(names []string) []string {
		out := make([]string, 0, len(names))
		for _, n := range names {
			if n != serverName {
				out = append(out, n)
			}
		}
		return out
	})
}

// updateAgentMCPServers reads an agent's current mcp_servers array,
// applies mutate, and writes the result back.
func (a *NaniteAdapter) updateAgentMCPServers(ctx context.Context, agentID string, mutate func([]string) []string) (*Agent, error) {
	agent, err := a.GetAgent(ctx, agentID)
	if err != nil {
		return nil, err
	}

	var names []string
	if agent.MCPServers != "" {
		if err := json.Unmarshal([]byte(agent.MCPServers), &names); err != nil {
			return nil, fmt.Errorf("failed to parse existing mcp_servers: %w", err)
		}
	}

	updatedNames := mutate(names)
	b, err := json.Marshal(updatedNames)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal mcp_servers: %w", err)
	}
	updatedJSON := string(b)

	return a.UpdateAgent(ctx, agentID, UpdateAgentRequest{MCPServers: &updatedJSON})
}

// naniteAgentReflex matches store.AgentReflex's wire shape
// (internal/store/agent_reflexes.go).
type naniteAgentReflex struct {
	ID                        string `json:"id"`
	AgentID                   string `json:"agent_id"`
	Name                      string `json:"name"`
	TriggerKind               string `json:"trigger_kind"`
	TriggerSpec               string `json:"trigger_spec"`
	ActionKind                string `json:"action_kind"`
	ActionSpec                string `json:"action_spec"`
	Status                    string `json:"status"`
	Priority                  int64  `json:"priority"`
	CreatedBy                 string `json:"created_by"`
	OptOutAllowed             bool   `json:"opt_out_allowed"`
	RecurrenceOverrideSeconds *int64 `json:"recurrence_override_seconds"`
}

func toReflex(nr naniteAgentReflex) *Reflex {
	return &Reflex{
		ID:                        nr.ID,
		AgentID:                   nr.AgentID,
		Name:                      nr.Name,
		TriggerKind:               nr.TriggerKind,
		TriggerSpec:               nr.TriggerSpec,
		ActionKind:                nr.ActionKind,
		ActionSpec:                nr.ActionSpec,
		Priority:                  nr.Priority,
		OptOutAllowed:             nr.OptOutAllowed,
		RecurrenceOverrideSeconds: nr.RecurrenceOverrideSeconds,
		Status:                    nr.Status,
		CreatedBy:                 nr.CreatedBy,
	}
}

// ListAgentReflexes implements AgentAdapter.ListAgentReflexes.
// GET /api/agents/{id}/reflexes
func (a *NaniteAdapter) ListAgentReflexes(ctx context.Context, agentID string) ([]Reflex, error) {
	var rows []naniteAgentReflex
	if err := a.doJSON(ctx, http.MethodGet, "/api/agents/"+agentID+"/reflexes", nil, &rows, http.StatusOK); err != nil {
		return nil, err
	}
	out := make([]Reflex, len(rows))
	for i, r := range rows {
		out[i] = *toReflex(r)
	}
	return out, nil
}

// naniteCreateReflexRequest matches the anonymous request struct in
// internal/api/reflexes.go's handleCreateAgentReflex.
type naniteCreateReflexRequest struct {
	Name                      string `json:"name"`
	TriggerKind               string `json:"trigger_kind"`
	TriggerSpec               string `json:"trigger_spec"`
	ActionKind                string `json:"action_kind"`
	ActionSpec                string `json:"action_spec"`
	Priority                  int64  `json:"priority"`
	OptOutAllowed             *bool  `json:"opt_out_allowed,omitempty"`
	RecurrenceOverrideSeconds *int64 `json:"recurrence_override_seconds,omitempty"`
}

// CreateAgentReflex implements AgentAdapter.CreateAgentReflex.
// POST /api/agents/{id}/reflexes
func (a *NaniteAdapter) CreateAgentReflex(ctx context.Context, agentID string, req CreateReflexRequest) (*Reflex, error) {
	naniteReq := naniteCreateReflexRequest{
		Name:                      req.Name,
		TriggerKind:               req.TriggerKind,
		TriggerSpec:               req.TriggerSpec,
		ActionKind:                req.ActionKind,
		ActionSpec:                req.ActionSpec,
		Priority:                  req.Priority,
		OptOutAllowed:             req.OptOutAllowed,
		RecurrenceOverrideSeconds: req.RecurrenceOverrideSeconds,
	}
	var created naniteAgentReflex
	if err := a.doJSON(ctx, http.MethodPost, "/api/agents/"+agentID+"/reflexes", naniteReq, &created, http.StatusOK, http.StatusCreated); err != nil {
		return nil, err
	}
	return toReflex(created), nil
}

// naniteUpdateReflexRequest matches the anonymous request struct in
// internal/api/reflexes.go's handlePatchAgentReflex.
type naniteUpdateReflexRequest struct {
	Name                      *string `json:"name,omitempty"`
	TriggerKind               *string `json:"trigger_kind,omitempty"`
	TriggerSpec               *string `json:"trigger_spec,omitempty"`
	ActionKind                *string `json:"action_kind,omitempty"`
	ActionSpec                *string `json:"action_spec,omitempty"`
	Priority                  *int64  `json:"priority,omitempty"`
	OptOutAllowed             *bool   `json:"opt_out_allowed,omitempty"`
	RecurrenceOverrideSeconds *int64  `json:"recurrence_override_seconds,omitempty"`
}

// UpdateAgentReflex implements AgentAdapter.UpdateAgentReflex.
// PATCH /api/agents/{id}/reflexes/{reflexId}
func (a *NaniteAdapter) UpdateAgentReflex(ctx context.Context, agentID, reflexID string, req UpdateReflexRequest) (*Reflex, error) {
	naniteReq := naniteUpdateReflexRequest{
		Name:                      req.Name,
		TriggerKind:               req.TriggerKind,
		TriggerSpec:               req.TriggerSpec,
		ActionKind:                req.ActionKind,
		ActionSpec:                req.ActionSpec,
		Priority:                  req.Priority,
		OptOutAllowed:             req.OptOutAllowed,
		RecurrenceOverrideSeconds: req.RecurrenceOverrideSeconds,
	}
	var updated naniteAgentReflex
	if err := a.doJSON(ctx, http.MethodPatch, "/api/agents/"+agentID+"/reflexes/"+reflexID, naniteReq, &updated, http.StatusOK); err != nil {
		return nil, err
	}
	return toReflex(updated), nil
}

// DeleteAgentReflex implements AgentAdapter.DeleteAgentReflex.
// DELETE /api/agents/{id}/reflexes/{reflexId}
func (a *NaniteAdapter) DeleteAgentReflex(ctx context.Context, agentID, reflexID string) error {
	return a.doJSON(ctx, http.MethodDelete, "/api/agents/"+agentID+"/reflexes/"+reflexID, nil, nil, http.StatusOK, http.StatusNoContent)
}

// slugify converts a string to a slug format (lowercase, spaces to hyphens).
func slugify(s string) string {
	// Simple slugification: lowercase and replace spaces with hyphens
	slug := ""
	for _, r := range s {
		if r == ' ' {
			slug += "-"
		} else if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			slug += string(r)
		} else if r >= 'A' && r <= 'Z' {
			slug += string(r + 32) // convert to lowercase
		}
	}
	return slug
}

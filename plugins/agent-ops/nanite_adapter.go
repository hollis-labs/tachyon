package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// NaniteAdapter implements AgentAdapter using Tether's mux aggregator
// (the mux_agent_* and mux_session_* MCP tools fronting Nanite).
//
// Design decision: We use the mux MCP tools rather than calling Nanite's
// HTTP/MCP API directly because:
//   - The mux layer provides a stable, already-aggregated interface
//   - It abstracts Nanite implementation details
//   - Future providers can be swapped in/out at the mux level
//   - The adapter remains provider-agnostic from the start
type NaniteAdapter struct {
	mcpURL     string       // MCP server URL (e.g., http://127.0.0.1:55970/mcp)
	httpClient *http.Client
}

// NewNaniteAdapter creates a new Nanite adapter using the mux MCP endpoint.
func NewNaniteAdapter(mcpURL string) *NaniteAdapter {
	return &NaniteAdapter{
		mcpURL:     mcpURL,
		httpClient: &http.Client{},
	}
}

// mcpRequest represents an MCP tool call request.
type mcpRequest struct {
	Method string                 `json:"method"`
	Params map[string]interface{} `json:"params"`
}

// mcpResponse represents an MCP tool call response.
type mcpResponse struct {
	Content []mcpContent `json:"content"`
	IsError bool         `json:"isError"`
}

// mcpContent represents a content block in the MCP response.
type mcpContent struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// callMCP makes an MCP tool call and returns the response text.
func (a *NaniteAdapter) callMCP(ctx context.Context, toolName string, params map[string]interface{}) (string, error) {
	reqBody := mcpRequest{
		Method: toolName,
		Params: params,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("failed to marshal MCP request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", a.mcpURL, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("failed to create HTTP request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("MCP call failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("MCP call returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var mcpResp mcpResponse
	if err := json.Unmarshal(respBody, &mcpResp); err != nil {
		return "", fmt.Errorf("failed to unmarshal MCP response: %w", err)
	}

	if mcpResp.IsError {
		if len(mcpResp.Content) > 0 {
			return "", fmt.Errorf("MCP tool error: %s", mcpResp.Content[0].Text)
		}
		return "", fmt.Errorf("MCP tool error (no details)")
	}

	if len(mcpResp.Content) == 0 {
		return "", fmt.Errorf("empty MCP response")
	}

	return mcpResp.Content[0].Text, nil
}

// ListAgents implements AgentAdapter.ListAgents using mux_agent_list.
func (a *NaniteAdapter) ListAgents(ctx context.Context) ([]Agent, error) {
	respText, err := a.callMCP(ctx, "mux_agent_list", map[string]interface{}{})
	if err != nil {
		return nil, fmt.Errorf("mux_agent_list failed: %w", err)
	}

	// Parse the response - the tool returns JSON array of agents
	var rawAgents []map[string]interface{}
	if err := json.Unmarshal([]byte(respText), &rawAgents); err != nil {
		return nil, fmt.Errorf("failed to parse agent list: %w", err)
	}

	agents := make([]Agent, 0, len(rawAgents))
	for _, raw := range rawAgents {
		agent := Agent{
			ID:       getString(raw, "id"),
			Name:     getString(raw, "name"),
			Layer:    getString(raw, "layer"),
			FilePath: getString(raw, "file_path"),
		}

		// Parse optional fields
		if sp, ok := raw["system_prompt"].(string); ok {
			agent.SystemPrompt = sp
		}
		if ap, ok := raw["agent_prompt"].(string); ok {
			agent.AgentPrompt = ap
		}
		if roles, ok := raw["roles"].([]interface{}); ok {
			agent.Roles = toStringSlice(roles)
		}
		if skills, ok := raw["skills"].([]interface{}); ok {
			agent.Skills = toStringSlice(skills)
		}

		agents = append(agents, agent)
	}

	return agents, nil
}

// GetAgent implements AgentAdapter.GetAgent using mux_agent_show.
func (a *NaniteAdapter) GetAgent(ctx context.Context, id string) (*Agent, error) {
	params := map[string]interface{}{
		"id": id,
	}

	respText, err := a.callMCP(ctx, "mux_agent_show", params)
	if err != nil {
		return nil, fmt.Errorf("mux_agent_show failed: %w", err)
	}

	var raw map[string]interface{}
	if err := json.Unmarshal([]byte(respText), &raw); err != nil {
		return nil, fmt.Errorf("failed to parse agent details: %w", err)
	}

	agent := &Agent{
		ID:       getString(raw, "id"),
		Name:     getString(raw, "name"),
		Layer:    getString(raw, "layer"),
		FilePath: getString(raw, "file_path"),
	}

	if sp, ok := raw["system_prompt"].(string); ok {
		agent.SystemPrompt = sp
	}
	if ap, ok := raw["agent_prompt"].(string); ok {
		agent.AgentPrompt = ap
	}
	if roles, ok := raw["roles"].([]interface{}); ok {
		agent.Roles = toStringSlice(roles)
	}
	if skills, ok := raw["skills"].([]interface{}); ok {
		agent.Skills = toStringSlice(skills)
	}

	return agent, nil
}

// CreateAgent implements AgentAdapter.CreateAgent using mux_agent_create.
func (a *NaniteAdapter) CreateAgent(ctx context.Context, req CreateAgentRequest) (*Agent, error) {
	params := map[string]interface{}{
		"id": req.ID,
	}

	if req.Name != "" {
		params["name"] = req.Name
	}
	if req.SystemPrompt != "" {
		params["system_prompt"] = req.SystemPrompt
	}
	if req.AgentPrompt != "" {
		params["agent_prompt"] = req.AgentPrompt
	}
	if len(req.Roles) > 0 {
		params["roles"] = strings.Join(req.Roles, ",")
	}
	if len(req.Skills) > 0 {
		params["skills"] = strings.Join(req.Skills, ",")
	}
	if req.Scope != "" {
		params["scope"] = req.Scope
	}
	if req.ProjectID != "" {
		params["project"] = req.ProjectID
	}

	if _, err := a.callMCP(ctx, "mux_agent_create", params); err != nil {
		return nil, fmt.Errorf("mux_agent_create failed: %w", err)
	}

	// After creation, fetch the full agent details
	return a.GetAgent(ctx, req.ID)
}

// UpdateAgent implements AgentAdapter.UpdateAgent using mux_agent_edit.
func (a *NaniteAdapter) UpdateAgent(ctx context.Context, id string, req UpdateAgentRequest) (*Agent, error) {
	params := map[string]interface{}{
		"id": id,
	}

	if req.Name != nil {
		params["name"] = *req.Name
	}
	if req.SystemPrompt != nil {
		params["system_prompt"] = *req.SystemPrompt
	}
	if req.AgentPrompt != nil {
		params["agent_prompt"] = *req.AgentPrompt
	}
	if req.Roles != nil {
		params["roles"] = strings.Join(req.Roles, ",")
	}
	if req.Skills != nil {
		params["skills"] = strings.Join(req.Skills, ",")
	}

	if _, err := a.callMCP(ctx, "mux_agent_edit", params); err != nil {
		return nil, fmt.Errorf("mux_agent_edit failed: %w", err)
	}

	// After update, fetch the current agent details
	return a.GetAgent(ctx, id)
}

// DeleteAgent implements AgentAdapter.DeleteAgent.
// Note: The mux_agent_* tools don't expose a delete operation,
// so this is not currently supported via the Nanite adapter.
func (a *NaniteAdapter) DeleteAgent(ctx context.Context, id string) error {
	return fmt.Errorf("delete operation not supported by Nanite adapter (mux_agent_* tools don't expose delete)")
}

// CreateSession implements AgentAdapter.CreateSession using mux_session_create.
func (a *NaniteAdapter) CreateSession(ctx context.Context, req CreateSessionRequest) (string, error) {
	params := map[string]interface{}{
		"launch_id": req.LaunchID,
	}

	if req.AgentFile != "" {
		params["agent_file"] = req.AgentFile
	}
	if req.AgentInline != "" {
		params["agent_inline"] = req.AgentInline
	}
	if req.BootProfile != "" {
		params["boot_profile"] = req.BootProfile
	}
	if req.BootPrompt != "" {
		params["boot_prompt"] = req.BootPrompt
	}
	if req.Override != "" {
		params["override"] = req.Override
	}
	if req.PromptAppend != "" {
		params["prompt_append"] = req.PromptAppend
	}
	if req.Injection != "" {
		params["injection"] = req.Injection
	}

	respText, err := a.callMCP(ctx, "mux_session_create", params)
	if err != nil {
		return "", fmt.Errorf("mux_session_create failed: %w", err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal([]byte(respText), &result); err != nil {
		return "", fmt.Errorf("failed to parse session create response: %w", err)
	}

	sessionID := getString(result, "session_id")
	if sessionID == "" {
		return "", fmt.Errorf("session_id not found in response")
	}

	return sessionID, nil
}

// LaunchSession implements AgentAdapter.LaunchSession using mux_session_launch.
func (a *NaniteAdapter) LaunchSession(ctx context.Context, sessionID string) (*LaunchResult, error) {
	params := map[string]interface{}{
		"session_id": sessionID,
	}

	respText, err := a.callMCP(ctx, "mux_session_launch", params)
	if err != nil {
		return nil, fmt.Errorf("mux_session_launch failed: %w", err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal([]byte(respText), &result); err != nil {
		return nil, fmt.Errorf("failed to parse launch response: %w", err)
	}

	return &LaunchResult{
		SessionID:     sessionID,
		WorkspacePath: getString(result, "workspace_path"),
		LogPath:       getString(result, "log_path"),
	}, nil
}

// Helper functions

func getString(m map[string]interface{}, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

func toStringSlice(arr []interface{}) []string {
	result := make([]string, 0, len(arr))
	for _, v := range arr {
		if s, ok := v.(string); ok {
			result = append(result, s)
		}
	}
	return result
}

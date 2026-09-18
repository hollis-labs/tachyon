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

// ListAgents implements AgentAdapter.ListAgents.
func (a *NaniteAdapter) ListAgents(ctx context.Context) ([]Agent, error) {
	// Use ?manageable=1 to exclude internal harness primitives
	url := a.baseURL + "/api/agents?manageable=1"

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GET /api/agents failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("GET /api/agents returned %d: %s", resp.StatusCode, string(body))
	}

	var naniteAgents []naniteAgent
	if err := json.NewDecoder(resp.Body).Decode(&naniteAgents); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	agents := make([]Agent, len(naniteAgents))
	for i, na := range naniteAgents {
		agents[i] = Agent{
			ID:           na.ID,
			Name:         na.Name,
			SystemPrompt: na.SystemPrompt,
			Layer:        na.ManageClass,
		}
	}

	return agents, nil
}

// GetAgent implements AgentAdapter.GetAgent.
func (a *NaniteAdapter) GetAgent(ctx context.Context, id string) (*Agent, error) {
	url := fmt.Sprintf("%s/api/agents/%s", a.baseURL, id)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GET /api/agents/%s failed: %w", id, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("GET /api/agents/%s returned %d: %s", id, resp.StatusCode, string(body))
	}

	// The GET endpoint wraps the agent in {"agent": ...}
	var wrapper struct {
		Agent naniteAgent `json:"agent"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&wrapper); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &Agent{
		ID:           wrapper.Agent.ID,
		Name:         wrapper.Agent.Name,
		SystemPrompt: wrapper.Agent.SystemPrompt,
		Layer:        wrapper.Agent.ManageClass,
	}, nil
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
		Description:  req.AgentPrompt,
	}

	body, err := json.Marshal(naniteReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	url := a.baseURL + "/api/agents"
	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := a.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("POST /api/agents failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("POST /api/agents returned %d: %s", resp.StatusCode, string(respBody))
	}

	var na naniteAgent
	if err := json.NewDecoder(resp.Body).Decode(&na); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &Agent{
		ID:           na.ID,
		Name:         na.Name,
		SystemPrompt: na.SystemPrompt,
		Layer:        na.ManageClass,
	}, nil
}

// UpdateAgent implements AgentAdapter.UpdateAgent.
func (a *NaniteAdapter) UpdateAgent(ctx context.Context, id string, req UpdateAgentRequest) (*Agent, error) {
	naniteReq := naniteUpdateAgentRequest{
		Name:         req.Name,
		SystemPrompt: req.SystemPrompt,
	}

	if req.AgentPrompt != nil {
		naniteReq.Description = req.AgentPrompt
	}

	body, err := json.Marshal(naniteReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	url := fmt.Sprintf("%s/api/agents/%s", a.baseURL, id)
	httpReq, err := http.NewRequestWithContext(ctx, "PUT", url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := a.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("PUT /api/agents/%s failed: %w", id, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("PUT /api/agents/%s returned %d: %s", id, resp.StatusCode, string(respBody))
	}

	// After update, fetch the current agent details
	return a.GetAgent(ctx, id)
}

// DeleteAgent implements AgentAdapter.DeleteAgent.
func (a *NaniteAdapter) DeleteAgent(ctx context.Context, id string) error {
	url := fmt.Sprintf("%s/api/agents/%s", a.baseURL, id)

	req, err := http.NewRequestWithContext(ctx, "DELETE", url, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("DELETE /api/agents/%s failed: %w", id, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("DELETE /api/agents/%s returned %d: %s", id, resp.StatusCode, string(body))
	}

	return nil
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

	body, err := json.Marshal(naniteReq)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	url := a.baseURL + "/api/sessions"
	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := a.httpClient.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("POST /api/sessions failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("POST /api/sessions returned %d: %s", resp.StatusCode, string(respBody))
	}

	var session naniteSession
	if err := json.NewDecoder(resp.Body).Decode(&session); err != nil {
		return "", fmt.Errorf("failed to decode response: %w", err)
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

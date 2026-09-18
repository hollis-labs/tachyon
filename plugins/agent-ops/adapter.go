// Package main defines the agent operational adapter interface.
package main

import "context"

// AgentAdapter defines the provider-agnostic interface for agent operations.
// Implementations wrap specific agent frameworks (Nanite, future providers)
// without the plugin core needing to know about provider-specific details.
type AgentAdapter interface {
	// ListAgents returns all manageable agents.
	ListAgents(ctx context.Context) ([]Agent, error)

	// GetAgent returns one agent's full definition by ID.
	GetAgent(ctx context.Context, id string) (*Agent, error)

	// CreateAgent creates a new agent.
	CreateAgent(ctx context.Context, req CreateAgentRequest) (*Agent, error)

	// UpdateAgent updates an existing agent's fields.
	UpdateAgent(ctx context.Context, id string, req UpdateAgentRequest) (*Agent, error)

	// DeleteAgent removes an agent definition.
	DeleteAgent(ctx context.Context, id string) error

	// CreateSession creates a session.
	// Returns the session ID.
	CreateSession(ctx context.Context, req CreateSessionRequest) (string, error)

	// LaunchSession starts a session (if the provider requires a separate launch step).
	// Returns launch result with session details.
	LaunchSession(ctx context.Context, sessionID string) (*LaunchResult, error)
}

// Agent represents an agent definition.
type Agent struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Slug         string `json:"slug,omitempty"`
	SystemPrompt string `json:"system_prompt,omitempty"`
	Description  string `json:"description,omitempty"`
	Tags         string `json:"tags,omitempty"`
	Icon         string `json:"icon,omitempty"`
	Status       string `json:"status,omitempty"` // enabled/disabled status
	Layer        string `json:"layer,omitempty"`  // management layer: managed/internal/plugin/external
	Editable     bool   `json:"editable"`
}

// CreateAgentRequest contains parameters for creating an agent.
type CreateAgentRequest struct {
	ID           string `json:"id,omitempty"`
	Name         string `json:"name"`
	SystemPrompt string `json:"system_prompt"`
	AgentPrompt  string `json:"agent_prompt,omitempty"` // Description/additional context
}

// UpdateAgentRequest contains parameters for updating an agent.
type UpdateAgentRequest struct {
	Name         *string `json:"name,omitempty"`
	SystemPrompt *string `json:"system_prompt,omitempty"`
	AgentPrompt  *string `json:"agent_prompt,omitempty"`
}

// CreateSessionRequest contains parameters for creating a session.
type CreateSessionRequest struct {
	ProjectID string `json:"project_id,omitempty"`
	Model     string `json:"model,omitempty"`
	Provider  string `json:"provider,omitempty"`
	AgentID   string `json:"agent_id,omitempty"`
}

// LaunchResult contains the result of launching a session.
type LaunchResult struct {
	SessionID string `json:"session_id"`
}

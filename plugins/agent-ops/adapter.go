// Package main defines the agent operational adapter interface.
package main

import "context"

// AgentAdapter defines the provider-agnostic interface for agent operations.
// Implementations wrap specific agent frameworks (Nanite, future providers)
// without the plugin core needing to know about provider-specific details.
type AgentAdapter interface {
	// ListAgents returns all agents across discovery layers.
	ListAgents(ctx context.Context) ([]Agent, error)

	// GetAgent returns one agent's full definition by ID.
	GetAgent(ctx context.Context, id string) (*Agent, error)

	// CreateAgent creates a new agent in the specified scope.
	CreateAgent(ctx context.Context, req CreateAgentRequest) (*Agent, error)

	// UpdateAgent updates an existing agent's fields.
	UpdateAgent(ctx context.Context, id string, req UpdateAgentRequest) (*Agent, error)

	// DeleteAgent removes an agent definition.
	// Not all providers may support deletion.
	DeleteAgent(ctx context.Context, id string) error

	// CreateSession creates a session from a launch profile.
	// Returns the session ID in created (not yet running) state.
	CreateSession(ctx context.Context, req CreateSessionRequest) (string, error)

	// LaunchSession starts a previously created session.
	// Transitions the session from created -> running.
	LaunchSession(ctx context.Context, sessionID string) (*LaunchResult, error)
}

// Agent represents an agent definition.
type Agent struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	SystemPrompt string   `json:"system_prompt,omitempty"`
	AgentPrompt  string   `json:"agent_prompt,omitempty"`
	Roles        []string `json:"roles,omitempty"`
	Skills       []string `json:"skills,omitempty"`
	Layer        string   `json:"layer,omitempty"`  // discovery layer: system/user/project
	FilePath     string   `json:"file_path,omitempty"`
}

// CreateAgentRequest contains parameters for creating an agent.
type CreateAgentRequest struct {
	ID           string   `json:"id"`
	Name         string   `json:"name,omitempty"`
	SystemPrompt string   `json:"system_prompt,omitempty"`
	AgentPrompt  string   `json:"agent_prompt,omitempty"`
	Roles        []string `json:"roles,omitempty"`
	Skills       []string `json:"skills,omitempty"`
	Scope        string   `json:"scope,omitempty"`  // project/user/system
	ProjectID    string   `json:"project_id,omitempty"`  // required when scope=project
}

// UpdateAgentRequest contains parameters for updating an agent.
type UpdateAgentRequest struct {
	Name         *string  `json:"name,omitempty"`
	SystemPrompt *string  `json:"system_prompt,omitempty"`
	AgentPrompt  *string  `json:"agent_prompt,omitempty"`
	Roles        []string `json:"roles,omitempty"`  // replaces existing
	Skills       []string `json:"skills,omitempty"` // replaces existing
}

// CreateSessionRequest contains parameters for creating a session.
type CreateSessionRequest struct {
	LaunchID      string `json:"launch_id"`
	AgentFile     string `json:"agent_file,omitempty"`
	AgentInline   string `json:"agent_inline,omitempty"`   // JSON-encoded agent
	BootProfile   string `json:"boot_profile,omitempty"`
	BootPrompt    string `json:"boot_prompt,omitempty"`
	Override      string `json:"override,omitempty"`       // JSON override object
	PromptAppend  string `json:"prompt_append,omitempty"`
	Injection     string `json:"injection,omitempty"`      // JSON LaunchInjection
}

// LaunchResult contains the result of launching a session.
type LaunchResult struct {
	SessionID     string `json:"session_id"`
	WorkspacePath string `json:"workspace_path,omitempty"`
	LogPath       string `json:"log_path,omitempty"`
}

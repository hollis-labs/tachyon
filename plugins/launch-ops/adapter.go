// Package main implements the launch-ops Tachyon plugin: two-phase
// agent execution orchestration (prepare intent, then execute).
//
// The launch module owns the lifecycle of a "launch" — an intent to
// start an agent session on a given provider with specific configuration.
// A launch moves through: prepared → executing → running → completed/failed/cancelled.
//
// The adapter pattern mirrors agent-ops: LaunchAdapter is the
// provider-neutral interface; NaniteLaunchAdapter is the first
// concrete backend calling Nanite's HTTP API for agent profiles
// and session creation.
package main

import (
	"context"
	"time"
)

// LaunchState is the lifecycle state of a launch.
type LaunchState string

const (
	LaunchStatePrepared  LaunchState = "prepared"
	LaunchStateExecuting LaunchState = "executing"
	LaunchStateRunning   LaunchState = "running"
	LaunchStateCompleted LaunchState = "completed"
	LaunchStateFailed    LaunchState = "failed"
	LaunchStateCancelled LaunchState = "cancelled"
)

// Launch is the domain entity representing a two-phase agent execution
// intent. The prepare phase creates it; the execute phase commits the
// side effects (session creation on the target provider).
type Launch struct {
	ID string `json:"id"`
	// Backend records the orchestration service separately from Provider.
	Backend   string      `json:"backend"`
	AgentID   string      `json:"agent_id"`
	AgentName string      `json:"agent_name,omitempty"`
	Provider  string      `json:"provider,omitempty"`
	Model     string      `json:"model,omitempty"`
	ProjectID string      `json:"project_id,omitempty"`
	State     LaunchState `json:"state"`
	SessionID string      `json:"session_id,omitempty"`
	Error     string      `json:"error,omitempty"`

	// Config holds provider-specific launch configuration. Opaque
	// to the launch module; the adapter passes it through to the
	// provider.
	Config map[string]any `json:"config,omitempty"`

	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	StartedAt *time.Time `json:"started_at,omitempty"`
	EndedAt   *time.Time `json:"ended_at,omitempty"`
}

// PrepareRequest is the payload for launch_prepare.
type PrepareRequest struct {
	// Backend optionally chooses nanite or tether; otherwise default_provider
	// applies (legacy provider=nanite/tether also selects that backend).
	Backend   string         `json:"backend,omitempty"`
	AgentID   string         `json:"agent_id"`
	Provider  string         `json:"provider,omitempty"`
	Model     string         `json:"model,omitempty"`
	ProjectID string         `json:"project_id,omitempty"`
	Config    map[string]any `json:"config,omitempty"`
}

// ExecuteRequest is the payload for launch_execute.
type ExecuteRequest struct {
	LaunchID string `json:"launch_id"`
}

// CancelRequest is the payload for launch_cancel.
type CancelRequest struct {
	LaunchID string `json:"launch_id"`
	Reason   string `json:"reason,omitempty"`
}

// ReadRequest is the payload for launch_read.
type ReadRequest struct {
	LaunchID string `json:"launch_id"`
}

// ListRequest is the payload for launch_list with optional filters.
type ListRequest struct {
	AgentID  string `json:"agent_id,omitempty"`
	State    string `json:"state,omitempty"`
	Provider string `json:"provider,omitempty"`
	Limit    int    `json:"limit,omitempty"`
}

// StatusRequest is the payload for launch_status (lightweight poll).
type StatusRequest struct {
	LaunchID string `json:"launch_id"`
}

// LaunchStatus is a lightweight status response for polling.
type LaunchStatus struct {
	LaunchID  string      `json:"launch_id"`
	State     LaunchState `json:"state"`
	SessionID string      `json:"session_id,omitempty"`
	Error     string      `json:"error,omitempty"`
	UpdatedAt time.Time   `json:"updated_at"`
}

// LaunchAdapter defines the provider-neutral operations for launch
// lifecycle management. Concrete adapters implement this for specific
// backends (Nanite, Tether, etc.).
type LaunchAdapter interface {
	// Prepare assembles a launch intent and persists it without
	// executing. Returns the prepared Launch with state "prepared".
	Prepare(ctx context.Context, req PrepareRequest) (*Launch, error)

	// Execute commits a prepared launch — kicks off agent execution
	// via the target provider. Transitions state from "prepared" to
	// "executing" then "running".
	Execute(ctx context.Context, req ExecuteRequest) (*Launch, error)

	// Cancel cancels a prepared intent or stops its running provider session.
	// Executing launches and unsupported provider stops return errors without
	// claiming cancellation.
	Cancel(ctx context.Context, req CancelRequest) (*Launch, error)

	// Read returns the full details of a launch by ID.
	Read(ctx context.Context, req ReadRequest) (*Launch, error)

	// List returns launches matching the given filters.
	List(ctx context.Context, req ListRequest) ([]Launch, error)

	// Status returns a lightweight status snapshot for polling.
	Status(ctx context.Context, req StatusRequest) (*LaunchStatus, error)
}

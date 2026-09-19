// Package main defines the agent operational adapter interface.
package main

import "context"

// AgentAdapter defines the provider-agnostic interface for agent operations.
// Implementations wrap specific agent frameworks (Nanite, future providers)
// without the plugin core needing to know about provider-specific details.
type AgentAdapter interface {
	// Capabilities declares which operations this adapter's provider
	// actually supports — modeled on Cerberus's connector.Capabilities
	// (pkg/connector/connector.go): a cheap, always-available boolean
	// declaration a consumer (the UI, a future provider picker) checks
	// before offering an action. This is not a substitute for each method
	// below still returning an honest error if called on an unsupported
	// operation — declaration and enforcement are deliberately redundant,
	// not one gating the other. A future read-only provider (e.g. a
	// binary/container-based agent runtime with no editable definition)
	// would return CanCreate/CanUpdate/CanDelete: false here while still
	// supporting the rest.
	Capabilities(ctx context.Context) (AgentCapabilities, error)

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

	// ListAgentTools returns the full discoverable tool catalog, each entry
	// reporting whether this agent currently holds a grant for it.
	ListAgentTools(ctx context.Context, agentID string) ([]Tool, error)

	// GrantAgentTool grants a known tool (by its known-tool ID, not name)
	// to the agent.
	GrantAgentTool(ctx context.Context, agentID, toolID string) error

	// RevokeAgentTool revokes a previously granted tool.
	RevokeAgentTool(ctx context.Context, agentID, toolID string) error

	// ListSkillCatalog returns every skill available to assign, regardless
	// of agent.
	ListSkillCatalog(ctx context.Context) ([]Skill, error)

	// ListAgentSkills returns the skills currently assigned to an agent,
	// with whatever grant/approval state each carries.
	ListAgentSkills(ctx context.Context, agentID string) ([]AgentSkill, error)

	// AssignAgentSkill assigns a skill (by ID) to an agent, making it
	// discoverable. This does not grant execution — see GrantAgentSkill.
	AssignAgentSkill(ctx context.Context, agentID, skillID string) error

	// RemoveAgentSkill un-assigns a skill from an agent.
	RemoveAgentSkill(ctx context.Context, agentID, skillID string) error

	// GetAgentSkillGrant reports whether an assigned skill is currently
	// approved for execution against its live content hash.
	GetAgentSkillGrant(ctx context.Context, agentID, skillSlug string) (*SkillGrantStatus, error)

	// GrantAgentSkill approves a skill for execution against its current
	// content hash.
	GrantAgentSkill(ctx context.Context, agentID, skillSlug, grantedBy string) error

	// RevokeAgentSkillGrant revokes execution approval for a skill without
	// un-assigning it.
	RevokeAgentSkillGrant(ctx context.Context, agentID, skillSlug string) error

	// ListMCPServerCatalog returns every MCP server registered with the
	// provider, for use in an attach picker.
	ListMCPServerCatalog(ctx context.Context) ([]MCPServer, error)

	// AttachAgentMCPServer attaches a registered MCP server to an agent.
	AttachAgentMCPServer(ctx context.Context, agentID, serverName string) (*Agent, error)

	// DetachAgentMCPServer detaches an MCP server from an agent.
	DetachAgentMCPServer(ctx context.Context, agentID, serverName string) (*Agent, error)

	// ListAgentReflexes returns the reflexes defined directly on an agent.
	ListAgentReflexes(ctx context.Context, agentID string) ([]Reflex, error)

	// CreateAgentReflex creates a new reflex on an agent.
	CreateAgentReflex(ctx context.Context, agentID string, req CreateReflexRequest) (*Reflex, error)

	// UpdateAgentReflex partially updates an existing reflex.
	UpdateAgentReflex(ctx context.Context, agentID, reflexID string, req UpdateReflexRequest) (*Reflex, error)

	// DeleteAgentReflex removes a reflex from an agent.
	DeleteAgentReflex(ctx context.Context, agentID, reflexID string) error

	// ListDurableAgents returns every long-running agent instance with its
	// current lifecycle status (e.g. sleeping, active, paused, failed).
	// Read-only for MVP — start/stop/pause lifecycle actions are post-MVP.
	ListDurableAgents(ctx context.Context) ([]DurableAgent, error)

	// GetDurableAgent returns one durable agent instance by ID.
	GetDurableAgent(ctx context.Context, id string) (*DurableAgent, error)

	// ListDurableAgentEvents returns recent lifecycle/activity events for a
	// durable agent instance, newest first.
	ListDurableAgentEvents(ctx context.Context, instanceID string) ([]DurableAgentEvent, error)

	// ListDurableAgentSessions returns the chat sessions currently or
	// previously attached to a durable agent instance.
	ListDurableAgentSessions(ctx context.Context, instanceID string) ([]DurableAgentSession, error)
}

// AgentCapabilities is a flat boolean declaration of what an adapter's
// provider supports, in the spirit of Cerberus's connector.Capabilities.
// Provider-level (can this adapter ever do X) rather than agent-level (see
// Agent.Editable for whether one specific row is editable within a
// provider that generally supports editing).
type AgentCapabilities struct {
	// Provider names which adapter this declaration came from (e.g.
	// "nanite"), for a UI surfacing multiple providers later.
	Provider            string `json:"provider"`
	CanCreate           bool   `json:"can_create"`
	CanUpdate           bool   `json:"can_update"`
	CanDelete           bool   `json:"can_delete"`
	CanGrantTools       bool   `json:"can_grant_tools"`
	CanAssignSkills     bool   `json:"can_assign_skills"`
	CanAttachMCPServers bool   `json:"can_attach_mcp_servers"`
	CanManageReflexes   bool   `json:"can_manage_reflexes"`
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

	// CanExecute reports whether this agent may be spawned as a subagent
	// worker (see docs/adding-an-agent.md's "Executable agents" section).
	CanExecute bool `json:"can_execute"`
	// MCPServers is a JSON array string of MCP server names attached to
	// this agent, e.g. `["helix-desk"]`. Kept as the raw JSON-string wire
	// shape Nanite uses rather than a decoded []string, since the plugin
	// round-trips it verbatim in most paths.
	MCPServers string `json:"mcp_servers,omitempty"`
	// RoleTools is a JSON array string of tool names seeded onto this
	// agent at create/update time.
	RoleTools string `json:"role_tools,omitempty"`
	// RoleSkills is a JSON array string of skill slugs seeded onto this
	// agent at create/update time.
	RoleSkills string `json:"role_skills,omitempty"`
}

// CreateAgentRequest contains parameters for creating an agent.
type CreateAgentRequest struct {
	ID           string `json:"id,omitempty"`
	Name         string `json:"name"`
	SystemPrompt string `json:"system_prompt"`
	// Description was previously wire-named "agent_prompt" — a name that
	// invited confusion with Nanite's own SlotAgent/SlotSystem context
	// slots (neither of which this field feeds; see
	// docs/architecture/context-assembly.md in Nanite — SlotAgent is
	// populated from SystemPrompt, not this field). Renamed to match what
	// it actually is: Nanite's plain `description` column, pure catalog
	// metadata never injected into the model's context.
	Description string `json:"description,omitempty"`
	CanExecute  bool   `json:"can_execute,omitempty"`
	MCPServers  string `json:"mcp_servers,omitempty"`
	RoleTools   string `json:"role_tools,omitempty"`
	RoleSkills  string `json:"role_skills,omitempty"`
}

// UpdateAgentRequest contains parameters for updating an agent.
type UpdateAgentRequest struct {
	Name         *string `json:"name,omitempty"`
	SystemPrompt *string `json:"system_prompt,omitempty"`
	Description  *string `json:"description,omitempty"`
	// Status is provider vocabulary, passed through verbatim. Nanite treats
	// "disabled" as hidden (out of its agent list and chat launch pickers)
	// and anything else, canonically "active", as visible.
	Status     *string `json:"status,omitempty"`
	CanExecute *bool   `json:"can_execute,omitempty"`
	MCPServers *string `json:"mcp_servers,omitempty"`
	RoleTools  *string `json:"role_tools,omitempty"`
	RoleSkills *string `json:"role_skills,omitempty"`
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

// Tool is a discoverable tool with this agent's grant state attached.
type Tool struct {
	// ID is the known-tool row ID to pass when granting this tool. Empty
	// when the tool hasn't been synced into the provider's known-tools
	// catalog yet (e.g. an MCP server registered since the provider last
	// restarted) — a UI should treat an empty ID as "not grantable until
	// a restart."
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Granted     bool   `json:"granted"`
}

// Skill is a catalog skill definition, independent of any agent.
type Skill struct {
	ID          string `json:"id"`
	Slug        string `json:"slug"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Category    string `json:"category,omitempty"`
	ContentHash string `json:"content_hash,omitempty"`
}

// AgentSkill is a skill catalog entry currently assigned to an agent.
// Assignment (discoverability) is independent of execution approval — see
// SkillGrantStatus for that.
type AgentSkill struct {
	SkillSlug   string `json:"skill_slug"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// SkillGrantStatus reports whether an agent's assigned skill is currently
// approved to execute against the skill's live content hash.
type SkillGrantStatus struct {
	AgentID             string `json:"agent_id"`
	SkillSlug           string `json:"skill_slug"`
	CurrentContentHash  string `json:"current_content_hash,omitempty"`
	ApprovedContentHash string `json:"approved_content_hash,omitempty"`
	GrantedAt           string `json:"granted_at,omitempty"`
	GrantedBy           string `json:"granted_by,omitempty"`
	// Status is one of: approved, grant_required, reapproval_required.
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}

// MCPServer is a registered MCP server catalog entry.
type MCPServer struct {
	Name          string `json:"name"`
	TransportType string `json:"transport_type,omitempty"`
}

// Reflex is a per-agent reflex definition.
type Reflex struct {
	ID                        string `json:"id"`
	AgentID                   string `json:"agent_id"`
	Name                      string `json:"name"`
	TriggerKind               string `json:"trigger_kind"`
	TriggerSpec               string `json:"trigger_spec"`
	ActionKind                string `json:"action_kind"`
	ActionSpec                string `json:"action_spec"`
	Priority                  int64  `json:"priority"`
	OptOutAllowed             bool   `json:"opt_out_allowed"`
	RecurrenceOverrideSeconds *int64 `json:"recurrence_override_seconds,omitempty"`
	Status                    string `json:"status,omitempty"`
	CreatedBy                 string `json:"created_by,omitempty"`
}

// CreateReflexRequest contains parameters for creating a reflex on an agent.
type CreateReflexRequest struct {
	Name                      string `json:"name"`
	TriggerKind               string `json:"trigger_kind"`
	TriggerSpec               string `json:"trigger_spec"`
	ActionKind                string `json:"action_kind"`
	ActionSpec                string `json:"action_spec"`
	Priority                  int64  `json:"priority,omitempty"`
	OptOutAllowed             *bool  `json:"opt_out_allowed,omitempty"`
	RecurrenceOverrideSeconds *int64 `json:"recurrence_override_seconds,omitempty"`
}

// UpdateReflexRequest contains partial-update parameters for a reflex.
// A nil field leaves the column untouched; RecurrenceOverrideSeconds set
// to 0 explicitly clears the override.
type UpdateReflexRequest struct {
	Name                      *string `json:"name,omitempty"`
	TriggerKind               *string `json:"trigger_kind,omitempty"`
	TriggerSpec               *string `json:"trigger_spec,omitempty"`
	ActionKind                *string `json:"action_kind,omitempty"`
	ActionSpec                *string `json:"action_spec,omitempty"`
	Priority                  *int64  `json:"priority,omitempty"`
	OptOutAllowed             *bool   `json:"opt_out_allowed,omitempty"`
	RecurrenceOverrideSeconds *int64  `json:"recurrence_override_seconds,omitempty"`
}

// DurableAgent is a long-running agent instance and its current lifecycle
// status — distinct from Agent, which is the reusable profile/definition an
// instance was launched from. Status is surfaced verbatim from the
// provider (Nanite's vocabulary today: sleeping, starting, active, paused,
// stopped, start_requested, stop_requested, resume_requested, failed,
// archived) rather than normalized to a fixed enum, the same
// provider-agnostic approach Agent.Status already takes.
type DurableAgent struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	Slug             string `json:"slug,omitempty"`
	ProfileID        string `json:"profile_id,omitempty"`
	LifecycleClass   string `json:"lifecycle_class,omitempty"`
	Provider         string `json:"provider,omitempty"`
	Model            string `json:"model,omitempty"`
	RuntimeKind      string `json:"runtime_kind,omitempty"`
	Status           string `json:"status"`
	CurrentSessionID string `json:"current_session_id,omitempty"`
	FailureReason    string `json:"failure_reason,omitempty"`
	CreatedAt        string `json:"created_at,omitempty"`
	UpdatedAt        string `json:"updated_at,omitempty"`
}

// DurableAgentEvent is one lifecycle/activity event recorded for a durable
// agent instance — what "activity at a glance" drills into.
type DurableAgentEvent struct {
	ID           string `json:"id"`
	EventType    string `json:"event_type"`
	StatusBefore string `json:"status_before,omitempty"`
	StatusAfter  string `json:"status_after,omitempty"`
	SessionID    string `json:"session_id,omitempty"`
	Source       string `json:"source,omitempty"`
	Message      string `json:"message,omitempty"`
	CreatedAt    string `json:"created_at"`
}

// DurableAgentSession is one chat session attached to a durable agent
// instance, with that session's own runtime state folded in.
type DurableAgentSession struct {
	SessionID     string `json:"session_id"`
	Relation      string `json:"relation,omitempty"`
	SessionStatus string `json:"session_status,omitempty"`
	Provider      string `json:"provider,omitempty"`
	Model         string `json:"model,omitempty"`
	RuntimeState  string `json:"runtime_state,omitempty"`
	AttachedAt    string `json:"attached_at,omitempty"`
}

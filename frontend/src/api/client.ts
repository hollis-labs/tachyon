import { createApiClient, type JsonObject } from "@hollis-labs/sysop-ui/api"

// Same-origin: the Go binary serves both this SPA and the API, so an empty
// baseUrl resolves every request against the current origin.
const http = createApiClient({ baseUrl: "" })

export interface HealthInfo {
  status: string
}

// What the active provider (Nanite today) actually supports — checked
// before offering an action instead of assuming Nanite's shape is the
// only shape a future non-Nanite adapter would have. Mirrors Cerberus's
// connector.Capabilities pattern: a cheap, always-available boolean
// declaration, not a substitute for the operation itself still returning
// an honest error if attempted while unsupported.
export interface AgentCapabilities {
  provider: string
  can_create: boolean
  can_update: boolean
  can_delete: boolean
  can_grant_tools: boolean
  can_assign_skills: boolean
  can_attach_mcp_servers: boolean
  can_manage_reflexes: boolean
}

export interface Agent {
  id: string
  name: string
  slug?: string
  description?: string
  tags?: string
  icon?: string
  status: string
  layer?: string
  editable: boolean
  system_prompt?: string
  can_execute: boolean
  // JSON-array-string fields, exactly as Nanite stores them
  // (e.g. `["helix-desk"]`) — parse with JSON.parse before rendering.
  mcp_servers?: string
  role_tools?: string
  role_skills?: string
}

export interface CreateAgentRequest {
  name: string
  system_prompt: string
  description?: string
  can_execute?: boolean
}

export interface UpdateAgentRequest {
  name?: string
  system_prompt?: string
  description?: string
  // Provider vocabulary — Nanite: "disabled" hides the agent, "active" shows it.
  status?: string
  can_execute?: boolean
}

export interface AgentTool {
  id: string
  name: string
  description?: string
  granted: boolean
}

export interface Skill {
  id: string
  slug: string
  name: string
  description?: string
  category?: string
  content_hash?: string
}

export interface AgentSkill {
  skill_slug: string
  name: string
  description?: string
}

export interface SkillGrantStatus {
  agent_id: string
  skill_slug: string
  current_content_hash?: string
  approved_content_hash?: string
  granted_at?: string
  granted_by?: string
  status: "approved" | "grant_required" | "reapproval_required"
  message?: string
}

export interface MCPServer {
  name: string
  transport_type?: string
}

export interface Reflex {
  id: string
  agent_id: string
  name: string
  trigger_kind: string
  trigger_spec: string
  action_kind: string
  action_spec: string
  priority: number
  opt_out_allowed: boolean
  recurrence_override_seconds?: number | null
  status?: string
  created_by?: string
}

export interface CreateReflexRequest {
  name: string
  trigger_kind: string
  trigger_spec: string
  action_kind: string
  action_spec: string
  priority?: number
  opt_out_allowed?: boolean
  recurrence_override_seconds?: number | null
}

export interface UpdateReflexRequest {
  name?: string
  trigger_kind?: string
  trigger_spec?: string
  action_kind?: string
  action_spec?: string
  priority?: number
  opt_out_allowed?: boolean
  recurrence_override_seconds?: number | null
}

// A long-running agent instance and its current lifecycle status — distinct
// from Agent, which is the reusable profile/definition an instance was
// launched from. Status is provider vocabulary, not a fixed enum (Nanite
// today: sleeping, starting, active, paused, stopped, start_requested,
// stop_requested, resume_requested, failed, archived).
export interface DurableAgent {
  id: string
  name: string
  slug?: string
  profile_id?: string
  lifecycle_class?: string
  provider?: string
  model?: string
  runtime_kind?: string
  status: string
  current_session_id?: string
  failure_reason?: string
  created_at?: string
  updated_at?: string
}

export interface DurableAgentEvent {
  id: string
  event_type: string
  status_before?: string
  status_after?: string
  session_id?: string
  source?: string
  message?: string
  created_at: string
}

export interface DurableAgentSession {
  session_id: string
  relation?: string
  session_status?: string
  provider?: string
  model?: string
  runtime_state?: string
  attached_at?: string
}

// Path segments are user- or provider-supplied identifiers (an MCP server
// name can contain spaces — e.g. "Agent Mux" — a skill slug or agent name
// could too), so every one of them is percent-encoded before landing in a
// URL template below.
const enc = encodeURIComponent

/**
 * Concrete API client — one method per endpoint.
 */
export const apiClient = {
  getHealth: () => http.get<HealthInfo>("/api/health"),
  getCapabilities: () => http.get<AgentCapabilities>("/api/capabilities"),

  // Agent operations. ApiClient only has get/post/request — PUT and DELETE
  // go through the request() escape hatch.
  listAgents: () => http.get<Agent[]>("/api/agents"),
  getAgent: (id: string) => http.get<Agent>(`/api/agents/${enc(id)}`),
  createAgent: (req: CreateAgentRequest) =>
    http.post<Agent>("/api/agents", req as unknown as JsonObject),
  updateAgent: (id: string, req: UpdateAgentRequest) =>
    http.request<Agent>(`/api/agents/${enc(id)}`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(req),
    }),
  deleteAgent: (id: string) => http.request<void>(`/api/agents/${enc(id)}`, { method: "DELETE" }),
  // Creates a session bound to this agent and returns its ID. Deliberately
  // minimal — Tachyon manages the agent, not the session (Tether's
  // authority); this doesn't open or project a session view.
  launchAgent: (id: string) =>
    http.post<{ session_id: string }>(`/api/agents/${enc(id)}/launch`, {}),

  // Tool grants: the list call returns the full discoverable catalog with
  // this agent's grant state folded in.
  listAgentTools: (agentId: string) => http.get<AgentTool[]>(`/api/agents/${enc(agentId)}/tools`),
  grantAgentTool: (agentId: string, toolId: string) =>
    http.post<{ agent_id: string; tool_id: string; granted: boolean }>(
      `/api/agents/${enc(agentId)}/tools`,
      { tool_id: toolId },
    ),
  revokeAgentTool: (agentId: string, toolId: string) =>
    http.request<void>(`/api/agents/${enc(agentId)}/tools/${enc(toolId)}`, { method: "DELETE" }),

  // Skill catalog (read-only from Tachyon).
  listSkillCatalog: () => http.get<Skill[]>("/api/skills"),

  // Agent skill assignment (discoverable, not yet approved to execute).
  listAgentSkills: (agentId: string) =>
    http.get<AgentSkill[]>(`/api/agents/${enc(agentId)}/skills`),
  assignAgentSkill: (agentId: string, skillId: string) =>
    http.post<{ agent_id: string; skill_id: string; assigned: boolean }>(
      `/api/agents/${enc(agentId)}/skills`,
      { skill_id: skillId },
    ),
  removeAgentSkill: (agentId: string, skillId: string) =>
    http.request<void>(`/api/agents/${enc(agentId)}/skills/${enc(skillId)}`, { method: "DELETE" }),

  // Agent skill grant (approval to execute against the skill's current
  // content hash).
  getAgentSkillGrant: (agentId: string, slug: string) =>
    http.get<SkillGrantStatus>(`/api/agents/${enc(agentId)}/skills/${enc(slug)}/grant`),
  grantAgentSkill: (agentId: string, slug: string, grantedBy: string) =>
    http.post<SkillGrantStatus>(`/api/agents/${enc(agentId)}/skills/${enc(slug)}/grant`, {
      granted_by: grantedBy,
    }),
  revokeAgentSkillGrant: (agentId: string, slug: string) =>
    http.request<void>(`/api/agents/${enc(agentId)}/skills/${enc(slug)}/grant`, {
      method: "DELETE",
    }),

  // MCP server catalog (read-only from Tachyon) and per-agent attach/detach.
  listMCPServerCatalog: () => http.get<MCPServer[]>("/api/mcp-servers"),
  attachAgentMCPServer: (agentId: string, serverName: string) =>
    http.post<Agent>(`/api/agents/${enc(agentId)}/mcp-servers`, { server_name: serverName }),
  detachAgentMCPServer: (agentId: string, serverName: string) =>
    http.request<void>(`/api/agents/${enc(agentId)}/mcp-servers/${enc(serverName)}`, {
      method: "DELETE",
    }),

  // Reflexes.
  listAgentReflexes: (agentId: string) =>
    http.get<Reflex[]>(`/api/agents/${enc(agentId)}/reflexes`),
  createAgentReflex: (agentId: string, req: CreateReflexRequest) =>
    http.post<Reflex>(`/api/agents/${enc(agentId)}/reflexes`, req as unknown as JsonObject),
  updateAgentReflex: (agentId: string, reflexId: string, req: UpdateReflexRequest) =>
    http.request<Reflex>(`/api/agents/${enc(agentId)}/reflexes/${enc(reflexId)}`, {
      method: "PATCH",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(req),
    }),
  deleteAgentReflex: (agentId: string, reflexId: string) =>
    http.request<void>(`/api/agents/${enc(agentId)}/reflexes/${enc(reflexId)}`, {
      method: "DELETE",
    }),

  // Durable agent instances (long-running agents) — read-only for MVP.
  listDurableAgents: () => http.get<DurableAgent[]>("/api/durable-agents"),
  getDurableAgent: (id: string) => http.get<DurableAgent>(`/api/durable-agents/${enc(id)}`),
  listDurableAgentEvents: (id: string) =>
    http.get<DurableAgentEvent[]>(`/api/durable-agents/${enc(id)}/events`),
  listDurableAgentSessions: (id: string) =>
    http.get<DurableAgentSession[]>(`/api/durable-agents/${enc(id)}/sessions`),
}

export type AppApiClient = typeof apiClient

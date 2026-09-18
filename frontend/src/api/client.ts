import { createApiClient } from "@hollis-labs/sysop-ui/api"

// Same-origin: the Go binary serves both this SPA and the API, so an empty
// baseUrl resolves every request against the current origin.
const http = createApiClient({ baseUrl: "" })

export interface HealthInfo {
  status: string
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
}

export interface CreateAgentRequest {
  name: string
  system_prompt: string
  agent_prompt?: string
}

export interface UpdateAgentRequest {
  name?: string
  system_prompt?: string
  agent_prompt?: string
}

/**
 * Concrete API client — one method per endpoint. The starter dashboard
 * only calls `getHealth`; add your application's endpoints here.
 */
export const apiClient = {
  getHealth: () => http.get<HealthInfo>("/api/health"),

  // Agent operations
  listAgents: () => http.get<Agent[]>("/api/agents"),
  getAgent: (id: string) => http.get<Agent>(`/api/agents/${id}`),
  createAgent: (req: CreateAgentRequest) => http.post<Agent>("/api/agents", req),
  updateAgent: (id: string, req: UpdateAgentRequest) => http.put<Agent>(`/api/agents/${id}`, req),
  deleteAgent: (id: string) => http.delete(`/api/agents/${id}`),
}

export type AppApiClient = typeof apiClient

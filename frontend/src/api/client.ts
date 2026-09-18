import { createApiClient, type JsonObject } from "@hollis-labs/sysop-ui/api"

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

  // Agent operations. ApiClient only has get/post/request — PUT and DELETE
  // go through the request() escape hatch.
  listAgents: () => http.get<Agent[]>("/api/agents"),
  getAgent: (id: string) => http.get<Agent>(`/api/agents/${id}`),
  createAgent: (req: CreateAgentRequest) => http.post<Agent>("/api/agents", req as unknown as JsonObject),
  updateAgent: (id: string, req: UpdateAgentRequest) =>
    http.request<Agent>(`/api/agents/${id}`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(req),
    }),
  deleteAgent: (id: string) =>
    http.request<void>(`/api/agents/${id}`, { method: "DELETE" }),
}

export type AppApiClient = typeof apiClient

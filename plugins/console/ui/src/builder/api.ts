import { hostFetch } from '@tachyon/host-sdk'
import type { AgentState, PolicyTag } from './vocab'

// Builder + Registry read model (internal/builderapi/openapi.yaml). GETs are open
// but prompt bodies may be operator-only: a 401/403 there is tolerated, not an error screen.
export const BUILDER_BASE = '/api/plugins/console/v1/builder'

export interface Summary {
  agents?: { total: number; active: number; disabled: number; retired?: number | null }
  launches?: { total: number; live?: number | null }
  library?: { prompts: number; skills: number; tool_servers?: number | null; policies: number }
  projects?: { total: number }
  open_changes?: number
  catalog?: { observed_at?: number; source_status?: 'ok' | 'stale' | 'unreachable' }
  capabilities?: { retired?: boolean; one_step_save?: boolean; resolved_by?: string }
}

export interface AgentRow {
  id: string
  name: string
  urn?: string | null
  provider: string
  model?: string | null
  enabled: boolean
  state: AgentState
  policy_tag?: PolicyTag | null
  launch_count: number
  layer?: string | null
  sha?: string | null
  owned_draft: boolean
}
export interface AgentList {
  agents: AgentRow[]
  total: number
  next_cursor?: string | null
}

export interface AgentDetail {
  id: string
  state: AgentState
  editable: boolean
  owned_draft: boolean
  identity: { id: string; name: string; description?: string; urn?: string | null; roles?: string[] }
  runtime: { provider: string; model?: string | null; extends?: string | null; abstract?: boolean; enabled?: boolean }
  prompt?: {
    parts?: { kind: string; id: string; rev: number; label: string; bytes: number; tokens_est: number }[]
    prompts?: string[]
    boot_fragments?: string[]
  }
  tools_skills?: { mcp_servers?: string[] | null; skills?: string[] }
  policy?: { permission_mode?: string | null; network?: string | null; default_sandbox?: string | null; mcp_scopes?: string[] }
}

export class BuilderError extends Error {
  constructor(public status: number, public code: string, message: string) {
    super(message)
  }
}

async function get<T>(path: string): Promise<T> {
  const res = await hostFetch(BUILDER_BASE + path, { headers: { Accept: 'application/json' } })
  if (!res.ok) {
    let code = 'error'
    let message = `Request failed (${res.status})`
    try {
      const b = (await res.json()) as { code?: string; message?: string }
      code = b.code ?? code
      message = b.message ?? message
    } catch {
      // keep the status text
    }
    throw new BuilderError(res.status, code, message)
  }
  return (await res.json()) as T
}

export interface AgentQuery {
  q?: string
  provider?: string
  state?: AgentState
  cursor?: string
}

export const builderApi = {
  summary: () => get<Summary>('/summary'),
  agents: (query: AgentQuery = {}) => {
    const p = new URLSearchParams()
    for (const [k, v] of Object.entries(query)) if (v) p.set(k, v)
    const qs = p.toString()
    return get<AgentList>(`/agents${qs ? `?${qs}` : ''}`)
  },
  agent: (id: string) => get<AgentDetail>(`/agents/${encodeURIComponent(id)}`),
}

export function errorMessage(e: unknown): string {
  if (e instanceof BuilderError && (e.status === 401 || e.status === 403)) return 'Operator access is needed to view this.'
  return e instanceof Error ? e.message : 'Could not load.'
}

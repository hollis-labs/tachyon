import { hostFetch } from '@tachyon/host-sdk'
import { operatorFetch } from '../operator/elevation'
import { BuilderError, errorMessage } from '../builder/api'

// Registry (Projects) read model and profile write: internal/builderapi/openapi.yaml.
export const REGISTRY_BASE = '/api/plugins/console/v1/registry/projects'

export type ProjectStatus = 'active' | 'paused' | 'archived'

export interface ProjectLink {
  state: 'linked' | 'unlinked'
  catalog_id?: string | null
  reason?: string | null
}

export interface ProjectRow {
  id: string
  display_name: string
  description: string
  tags: string[]
  status: string
  link: ProjectLink
  counts?: { agents?: number | null; launches?: number | null; planner_items?: number | null; tether_sessions?: number | null }
}

export interface Project {
  id: string
  identity: { display_name: string; description: string; tags: string[]; status: string }
  guidance: { entry_points: string[]; guidelines: string }
  link: ProjectLink
  catalog?: { repo_root?: string | null; workspace?: Record<string, unknown>; mcp_servers?: string[] | null; sha?: string } | null
  consumed_by?: {
    agents?: { id: string; name: string }[] | null
    launches?: { id: string }[] | null
    planner_items?: { open: number; total: number | null } | null
    tether_sessions?: { live: number; total: number | null } | null
  }
  soon?: string[]
}

export type ProjectPatch = Partial<{ display_name: string; description: string; tags: string[]; guidelines: string; entry_points: string[]; status: ProjectStatus }>

async function call<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await (init?.method && init.method !== 'GET' ? operatorFetch : hostFetch)(REGISTRY_BASE + path, { ...init, headers: { Accept: 'application/json', ...(init?.body ? { 'Content-Type': 'application/json' } : {}) } })
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

export const registryApi = {
  list: (q = '') => call<{ projects: ProjectRow[]; next_cursor?: string | null }>(q ? `?q=${encodeURIComponent(q)}` : ''),
  get: (id: string) => call<Project>(`/${encodeURIComponent(id)}`),
  patch: (id: string, patch: ProjectPatch) => call<Project>(`/${encodeURIComponent(id)}`, { method: 'PATCH', body: JSON.stringify(patch) }),
}

export { errorMessage }

const same = (a: unknown, b: unknown) => JSON.stringify(a) === JSON.stringify(b)

/** Only the fields that differ from the saved profile; the backend rejects unknown and null fields. */
export function profilePatch(p: Project, draft: ProjectPatch): ProjectPatch {
  const current: Record<keyof ProjectPatch, unknown> = {
    display_name: p.identity.display_name,
    description: p.identity.description,
    tags: p.identity.tags,
    status: p.identity.status,
    guidelines: p.guidance.guidelines,
    entry_points: p.guidance.entry_points,
  }
  const out: Record<string, unknown> = {}
  for (const [k, v] of Object.entries(draft)) if (v !== undefined && !same(v, current[k as keyof ProjectPatch])) out[k] = v
  return out as ProjectPatch
}

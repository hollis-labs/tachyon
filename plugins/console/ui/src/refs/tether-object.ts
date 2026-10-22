import type { KindResolver, RefEntity, ResolveOutcome } from './types'

type Get = (path: string, signal?: AbortSignal) => Promise<Response>

const BASE = '/api/plugins/console/v1/tether'
const str = (v: unknown): string => (typeof v === 'string' ? v.trim() : '')

const PLAN_TONE: Record<string, 'success' | 'info' | 'warning' | 'danger' | 'neutral'> = {
  applied: 'success', approved: 'info', planned: 'info', invalid: 'danger', expired: 'warning', rolled_back: 'neutral',
}
const AGENT_TONE: Record<string, 'success' | 'info' | 'warning' | 'danger' | 'neutral'> = {
  active: 'success', deprecated: 'neutral', archived: 'neutral', merged: 'neutral',
}

export function planEntity(p: Record<string, unknown>): RefEntity {
  const state = str(p.state)
  const files = Array.isArray(p.files) ? (p.files as Record<string, unknown>[]) : []
  const total = typeof p.file_count === 'number' ? p.file_count : files.length
  const fields = [
    { label: 'Task', value: str(p.task_id) },
    { label: 'Valid', value: p.validation_ok === true ? 'yes' : p.validation_ok === false ? 'no' : '' },
    { label: 'Created', value: str(p.created_at) },
    { label: 'Expires', value: str(p.expires_at) },
    { label: 'Applied', value: str(p.applied_at) },
    { label: 'Files', value: total ? String(total) : '' },
  ].filter((f) => f.value)
  const lines = files.map((f) => `${str(f.action) || 'change'} ${str(f.path)}`)
  if (total > files.length) lines.push(`… and ${total - files.length} more`)
  const parts = [str(p.validation_error), lines.join('\n')].filter(Boolean)
  return {
    title: str(p.name) || str(p.plan_id),
    subtitle: str(p.plan_id),
    status: state ? { label: state.replace(/_/g, ' '), tone: PLAN_TONE[state] ?? 'neutral' } : undefined,
    description: parts.length ? parts.join('\n\n') : undefined,
    fields,
    updatedAt: str(p.applied_at) || str(p.created_at) || undefined,
  }
}

export function agentEntity(a: Record<string, unknown>): RefEntity {
  const status = str(a.status)
  const tags = Array.isArray(a.tags) ? a.tags.map(str).filter(Boolean).join(', ') : ''
  const fields = [
    { label: 'Role', value: str(a.role) },
    { label: 'Title', value: str(a.title) },
    { label: 'Project', value: str(a.project) },
    { label: 'Health', value: str(a.health_status) },
    { label: 'Last seen', value: str(a.last_seen_at) },
    { label: 'Tags', value: tags },
    { label: 'URN', value: str(a.urn), mono: true },
  ].filter((f) => f.value)
  return {
    title: str(a.display_name) || str(a.urn),
    status: status ? { label: status, tone: AGENT_TONE[status] ?? 'neutral' } : undefined,
    description: str(a.description) || undefined,
    fields,
    updatedAt: str(a.updated_at) || undefined,
  }
}

/** Reads one hub object through Tachyon's operator-only projection route. */
function resolver(route: string, entity: (o: Record<string, unknown>) => RefEntity, get: Get): KindResolver {
  return {
    async resolveMany(ids, { signal }) {
      const out: Record<string, ResolveOutcome> = {}
      await Promise.all(ids.map(async (id) => {
        const res = await get(`${BASE}/${route}/${encodeURIComponent(id)}`, signal)
        if (res.status === 404) {
          out[id] = { status: 'not-found' }
          return
        }
        if (res.status === 401 || res.status === 403) throw new Error('Operator access is needed to read Tether objects.')
        if (!res.ok) throw new Error(`Tether object unavailable (${res.status}).`)
        out[id] = { status: 'found', entity: entity((await res.json()) as Record<string, unknown>) }
      }))
      return out
    },
  }
}

export const createTetherPlanResolver = (get: Get) => resolver('plans', planEntity, get)
export const createTetherAgentResolver = (get: Get) => resolver('agents', agentEntity, get)

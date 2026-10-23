import type { Epic, PlanDetail, Project, Sprint } from '@tachyon/torque-client'
import { taskStatusPill } from './torque-task'
import type { KindResolver, RefEntity, ResolveOutcome, StatusTone } from './types'

const STATUS_TONE: Record<string, StatusTone> = { active: 'success', completed: 'info', inactive: 'neutral' }
const pill = (status: string) => ({ label: status.replace(/_/g, ' '), tone: STATUS_TONE[status] ?? 'neutral' })
const present = (fields: { label: string; value: string | number | null | undefined; mono?: boolean }[]) =>
  fields.flatMap((f) => (f.value === null || f.value === undefined || f.value === '' ? [] : [{ ...f, value: String(f.value) }]))

export function epicEntity(e: Epic): RefEntity {
  return {
    title: e.name, chipLabel: `Epic: ${e.name}`, subtitle: e.id, status: pill(e.status),
    fields: present([{ label: 'Priority', value: e.priority === null ? null : `P${e.priority}` }, { label: 'Project', value: e.project_id, mono: true }]),
    updatedAt: e.updated_at,
  }
}

export function projectEntity(p: Project): RefEntity {
  return {
    title: p.name, chipLabel: `Project: ${p.name}`, subtitle: p.id, status: pill(p.status), updatedAt: p.updated_at,
  }
}

export function sprintEntity(s: Sprint): RefEntity {
  return {
    title: s.name, chipLabel: `Sprint: ${s.name}`, subtitle: s.id, status: pill(s.status), description: s.goal || undefined,
    fields: present([
      { label: 'Approval', value: s.approval_mode }, { label: 'Project', value: s.project_id, mono: true }, { label: 'Epic', value: s.epic_id, mono: true },
      { label: 'Started', value: s.started_at }, { label: 'Ended', value: s.ended_at },
    ]),
    updatedAt: s.updated_at,
  }
}

export function planEntity(d: PlanDetail): RefEntity {
  const phases = d.plan.phases.slice().sort((a, b) => a.order - b.order)
  const lines = phases.map((p) => {
    const r = d.progress.by_phase[p.id]
    return `${p.order}. ${p.name}${r ? ` (${r.done}/${r.total} done${r.blocked ? `, ${r.blocked} blocked` : ''})` : ''}`
  })
  return {
    title: d.task.title, chipLabel: `Plan: ${d.task.title}`, subtitle: d.task.id,
    status: taskStatusPill(d.task.status),
    description: lines.length ? lines.join('\n') : undefined,
    fields: present([{ label: 'Progress', value: `${d.progress.done}/${d.progress.total_children} done` }, { label: 'Blocked', value: d.progress.blocked || null }]),
    updatedAt: d.task.updated_at,
  }
}

/**
 * The server projection keeps ids, status codes and short names only (no descriptions, paths or acceptance text).
 * Epics, projects, sprints and plans were chips without a preview: clicking one
 * left the console for the entity's page. One resolver per kind reads the entity
 * through the existing Torque client, and the existing drawer shows the card.
 */
export function createTorqueContainerResolver<T>(read: (id: string, signal?: AbortSignal) => Promise<T>, entity: (v: T) => RefEntity): KindResolver {
  return {
    async resolveMany(ids, { signal }) {
      const out: Record<string, ResolveOutcome> = {}
      await Promise.all(ids.map(async (id) => {
        try {
          out[id] = { status: 'found', entity: entity(await read(id, signal)) }
        } catch (e) {
          if ((e as { status?: number }).status === 404) out[id] = { status: 'not-found' }
          else throw e
        }
      }))
      return out
    },
  }
}

type Get = (path: string, signal?: AbortSignal) => Promise<Response>

/**
 * Reads one Torque object through Tachyon's operator-gated, projected preview route
 * (never the raw /v1 passthrough). Quiet on purpose: a missing elevation is an
 * error state on the chip, not a prompt.
 */
export function previewRead<T>(collection: 'epics' | 'projects' | 'sprints' | 'plans', get: Get): (id: string, signal?: AbortSignal) => Promise<T> {
  return async (id, signal) => {
    const res = await get(`/api/plugins/console/v1/preview/${collection}/${encodeURIComponent(id)}`, signal)
    if (res.status === 404) throw Object.assign(new Error('Not found'), { status: 404 })
    if (res.status === 401 || res.status === 403) throw new Error('Operator access is needed to read Torque objects.')
    if (!res.ok) throw new Error(`Torque object unavailable (${res.status}).`)
    return (await res.json()) as T
  }
}

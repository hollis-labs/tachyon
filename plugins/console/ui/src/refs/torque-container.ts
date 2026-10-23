import type { Epic, PlanDetail, Project, Sprint } from '@tachyon/torque-client'
import { taskStatusPill } from './torque-task'
import type { KindResolver, RefEntity, ResolveOutcome, StatusTone } from './types'

const STATUS_TONE: Record<string, StatusTone> = { active: 'success', completed: 'info', inactive: 'neutral' }
const pill = (status: string) => ({ label: status.replace(/_/g, ' '), tone: STATUS_TONE[status] ?? 'neutral' })
const present = (fields: { label: string; value: string | number | null | undefined; mono?: boolean }[]) =>
  fields.flatMap((f) => (f.value === null || f.value === undefined || f.value === '' ? [] : [{ ...f, value: String(f.value) }]))

export function epicEntity(e: Epic): RefEntity {
  return {
    title: e.name, chipLabel: `Epic: ${e.name}`, subtitle: e.id, status: pill(e.status), description: e.description || undefined,
    fields: present([{ label: 'Priority', value: e.priority === null ? null : `P${e.priority}` }, { label: 'Project', value: e.project_id, mono: true }]),
    updatedAt: e.updated_at,
  }
}

export function projectEntity(p: Project): RefEntity {
  return {
    title: p.name, chipLabel: `Project: ${p.name}`, subtitle: p.id, status: pill(p.status), description: p.description || undefined,
    fields: present([{ label: 'Repo', value: p.repo_path, mono: true }]), updatedAt: p.updated_at,
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
    return `${p.order}. ${p.name}${r ? ` (${r.done}/${r.total} done${r.blocked ? `, ${r.blocked} blocked` : ''})` : ''}${p.acceptance ? `: ${p.acceptance}` : ''}`
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

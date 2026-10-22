import type { PlannerTaskBrief } from '@tachyon/torque-client'
import type { KindResolver, ResolveOutcome, StatusTone } from './types'

const TASK_TONE: Record<string, StatusTone> = {
  done: 'success',
  in_progress: 'info',
  review: 'info',
  blocked: 'danger',
  failed: 'danger',
  todo: 'neutral',
}

/** The status pill for a Torque task status. */
export function taskStatusPill(status: string): { label: string; tone: StatusTone } {
  return { label: status.replace(/_/g, ' '), tone: TASK_TONE[status] ?? 'neutral' }
}

/**
 * The real adapter for `torque.task`: wraps the existing
 * `api.plannerTaskTitles` (the endpoint behind usePlannerTaskTitles), so task
 * references resolve against the live Torque with no new backend work.
 */
export function createTorqueTaskResolver(
  fetchTitles: (ids: string[], signal?: AbortSignal) => Promise<Record<string, PlannerTaskBrief | null>>,
): KindResolver {
  return {
    async resolveMany(ids, { signal }) {
      const titles = await fetchTitles(ids, signal)
      const out: Record<string, ResolveOutcome> = {}
      for (const id of ids) {
        const t = titles[id]
        out[id] = t
          ? {
              status: 'found',
              entity: {
                title: t.title,
                chipLabel: `Task: ${t.title}`,
                status: taskStatusPill(t.status),
                fields: [{ label: 'Priority', value: `P${t.priority}` }],
                updatedAt: t.updated_at,
              },
            }
          : { status: 'not-found' }
      }
      return out
    },
  }
}

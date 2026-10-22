import type { PlannerItem } from '@tachyon/torque-client'
import type { KindResolver, RefEntity, ResolveOutcome, StatusTone } from './types'

const TONE: Record<string, StatusTone> = {
  approved: 'success', accepted: 'success', done: 'success', resolved: 'success', closed: 'success', achieved: 'success',
  ready: 'info', active: 'info', open: 'info', proposed: 'info', mitigating: 'info',
  paused: 'warning', deferred: 'warning', blocked: 'warning',
  rejected: 'danger', declined: 'danger', archived: 'neutral',
}

const text = (v: unknown): string => (typeof v === 'string' ? v.trim() : typeof v === 'number' ? String(v) : '')

// Prose fields in the order they read well; the first is the lead, the rest follow as paragraphs.
const LEAD = ['tldr', 'description', 'outcome', 'summary', 'notes']
const MORE: [string, string][] = [['rationale', 'Why'], ['mitigation', 'Mitigation'], ['next_step', 'Next step'], ['context', 'Context']]

function describe(item: PlannerItem): string | undefined {
  const lead = LEAD.map((k) => text(item[k])).find(Boolean)
  const parts = lead ? [lead] : []
  for (const [k, label] of MORE) {
    const v = text(item[k])
    if (v && v !== lead) parts.push(`${label}: ${v}`)
  }
  return parts.length ? parts.join('\n\n') : undefined
}

const FIELDS: [string, string][] = [['priority', 'Priority'], ['accountable', 'Accountable'], ['executing_owner', 'Owner'], ['approved_by', 'Approved by'], ['likelihood', 'Likelihood'], ['impact', 'Impact']]

/** A planner item as a preview card: title, status, a lead paragraph and the few fields worth a glance. */
export function plannerEntity(item: PlannerItem): RefEntity {
  const status = text(item.status)
  const fields = FIELDS.flatMap(([k, label]) => {
    const v = text(item[k])
    return v && v !== '0' ? [{ label, value: v }] : []
  })
  const comments = Array.isArray(item.comments) ? item.comments.length : 0
  if (comments > 0) fields.push({ label: 'Comments', value: String(comments) })
  return {
    title: text(item.title) || text(item.name) || text(item.id),
    status: status ? { label: status.replace(/_/g, ' '), tone: TONE[status] ?? 'neutral' } : undefined,
    description: describe(item),
    fields,
    updatedAt: text(item.updated) || undefined,
  }
}

/** Resolves PR-nn, DEC-n, RK-n, IN-nn and WS-… ids through the planner's reference lookup. */
export function createPlannerResolver(resolve: (ref: string, signal?: AbortSignal) => Promise<{ item: PlannerItem }>): KindResolver {
  return {
    async resolveMany(ids, { signal }) {
      const out: Record<string, ResolveOutcome> = {}
      await Promise.all(ids.map(async (id) => {
        try {
          out[id] = { status: 'found', entity: plannerEntity((await resolve(id, signal)).item) }
        } catch (err) {
          const status = (err as { status?: number }).status
          const code = (err as { code?: string }).code
          // Unknown or ambiguous: not found. Anything else fails the batch so the card can retry.
          if (status === 404 || code === 'not_found' || status === 409) out[id] = { status: 'not-found' }
          else throw err
        }
      }))
      return out
    },
  }
}

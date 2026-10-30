import { KINDS } from '../events/types'
import type { Alert } from './types'

/**
 * Alerts that block something on the operator: an approval waiting for a decision, an agent
 * waiting on them, or anything already at error or above (failed task, escalation). Everything
 * else is information. Client-side until the mapper carries a "needs you" flag on the kind.
 */
export function needsYou(a: Alert): boolean {
  if (a.severity === 'error' || a.severity === 'critical') return true
  return a.kind === KINDS.approvalRequested || a.kind === KINDS.agentState
}

export function dayLabel(ts: number, now: number): string {
  const start = (t: number) => { const d = new Date(t); return new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime() }
  const diff = Math.round((start(now) - start(ts)) / 86_400_000)
  if (diff <= 0) return 'Today'
  if (diff === 1) return 'Yesterday'
  return new Date(ts).toLocaleDateString([], { month: 'short', day: 'numeric' })
}

export interface Group<T> { id: string; label: string; items: T[] }

/** Active: "Needs you" first, then "Everything else". History: one group per day, newest first. */
export function groupActive<T extends { alert: Alert }>(rows: T[]): Group<T>[] {
  const need = rows.filter((r) => needsYou(r.alert))
  const rest = rows.filter((r) => !needsYou(r.alert))
  return [
    { id: 'needs', label: 'Needs you', items: need },
    { id: 'rest', label: 'Everything else', items: rest },
  ].filter((g) => g.items.length)
}

export function groupHistory<T extends { alert: Alert }>(rows: T[], now: number): Group<T>[] {
  const out: Group<T>[] = []
  for (const r of rows) {
    const label = dayLabel(r.alert.ts, now)
    const last = out.at(-1)
    if (last?.label === label) last.items.push(r)
    else out.push({ id: label, label, items: [r] })
  }
  return out
}

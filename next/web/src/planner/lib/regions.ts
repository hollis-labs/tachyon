import { useSyncExternalStore, type ReactNode } from 'react'

/** Plugin contribution points of the Planner shell (flow spec "Plugin contribution points"). */
export const REGIONS = [
  'planner.lens',
  'planner.spine.item.menu',
  'planner.focus.panel',
  'planner.focus.timeline.source',
  'planner.context.tab',
  'planner.palette.command',
  'planner.card.action',
] as const
export type PlannerRegion = (typeof REGIONS)[number]

export interface Contribution {
  id: string
  /** Lower sorts first; the host's own entries keep their place before plugin ones. */
  order?: number
  label?: string
  render?: (ctx: Record<string, unknown>) => ReactNode
  data?: unknown
}

let store: Readonly<Record<PlannerRegion, readonly Contribution[]>> = Object.fromEntries(REGIONS.map((r) => [r, []])) as unknown as typeof store
const listeners = new Set<() => void>()

/** Adds a contribution; returns the function that removes it. Re-registering an id replaces it. */
export function registerContribution(region: PlannerRegion, c: Contribution): () => void {
  const next = [...store[region].filter((x) => x.id !== c.id), c].sort((a, b) => (a.order ?? 0) - (b.order ?? 0))
  store = { ...store, [region]: next }
  listeners.forEach((l) => l())
  return () => {
    store = { ...store, [region]: store[region].filter((x) => x !== c) }
    listeners.forEach((l) => l())
  }
}

export function contributions(region: PlannerRegion): readonly Contribution[] {
  return store[region]
}

export function useContributions(region: PlannerRegion): readonly Contribution[] {
  return useSyncExternalStore(
    (cb) => (listeners.add(cb), () => listeners.delete(cb)),
    () => store[region],
  )
}

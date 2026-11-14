import { describe, expect, it } from 'vitest'
import { REGIONS, contributions, registerContribution } from '@planner/lib/regions'

describe('plugin regions', () => {
  it('registers a test contribution in every region, in order, and removes it', () => {
    const undo = REGIONS.map((r) => registerContribution(r, { id: 'test', order: 5, label: r }))
    registerContribution('planner.lens', { id: 'first', order: 1 })
    expect(contributions('planner.lens').map((c) => c.id)).toEqual(['first', 'test'])
    for (const r of REGIONS) expect(contributions(r).some((c) => c.id === 'test')).toBe(true)
    undo.forEach((u) => u())
    expect(contributions('planner.card.action')).toHaveLength(0)
  })
})

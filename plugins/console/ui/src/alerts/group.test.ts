import { describe, expect, it } from 'vitest'
import { dayLabel, groupActive, groupHistory, needsYou } from './group'
import type { Alert } from './types'

const a = (over: Partial<Alert>): Alert => ({ id: 'x', severity: 'info', icon: 'info', title: 't', source: 's', ts: 0, kind: 'k', ...over })
const NOW = Date.parse('2026-10-30T12:00:00')

describe('alert grouping', () => {
  it('puts approvals, waiting agents and errors under Needs you', () => {
    expect(needsYou(a({ kind: 'approval.requested' }))).toBe(true)
    expect(needsYou(a({ kind: 'agent.state' }))).toBe(true)
    expect(needsYou(a({ severity: 'error' }))).toBe(true)
    expect(needsYou(a({ kind: 'pr.merged' }))).toBe(false)
    const g = groupActive([{ alert: a({ id: '1', kind: 'pr.merged', ts: NOW }) }, { alert: a({ id: '2', kind: 'approval.requested', ts: NOW }) }], NOW)
    expect(g.map((x) => [x.label, x.items.length])).toEqual([['Needs you', 1], ['Today', 1]])
  })
  it('groups history by day, newest first order preserved', () => {
    const rows = [{ alert: a({ id: '1', ts: NOW - 1000 }) }, { alert: a({ id: '2', ts: NOW - 86_400_000 }) }, { alert: a({ id: '3', ts: NOW - 86_400_000 - 5 }) }]
    expect(groupHistory(rows, NOW).map((g) => [g.label, g.items.length])).toEqual([['Today', 1], ['Yesterday', 2]])
    expect(dayLabel(NOW, NOW)).toBe('Today')
  })
})

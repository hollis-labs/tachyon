import { describe, expect, it } from 'vitest'
import { themeCountsText, themeOwners, themeProgressFraction, themeProgressText, themeRowActions, themeWaiting, verifiedAge } from '@planner/lib/planner'
import type { ThemeRow, ThemeSectionKey } from '@planner/api/types'

const NOW = Date.parse('2026-10-21T12:00:00Z')
const row = (over: Partial<ThemeRow> & { id: string; title: string; section: ThemeSectionKey }): ThemeRow => ({
  source: 'theme', db: 'themes', rev: 3, status: 'approved', updated: '2026-10-21T10:00:00Z', blocked_count: 0, unmet_dependencies: 0,
  progress: { available: true, done: 12, total: 40, open: 20, linked: true }, ...over,
})

describe('theme progress helpers', () => {
  it('never reports 0/0 or a misleading count', () => {
    expect(themeProgressText({ available: true, done: 12, total: 40 })).toBe('12/40 done')
    expect(themeProgressText({ available: true, total: 0, linked: false })).toBe('no tasks linked')
    expect(themeProgressText({ available: false })).toBe('progress unavailable')
    expect(themeProgressFraction({ available: true, done: 10, total: 40 })).toBe(0.25)
    expect(themeProgressFraction({ available: true, done: 0, total: 0 })).toBeNull()
    expect(themeProgressFraction({ available: false })).toBeNull()
  })
  it('flags an unverified or stale record', () => {
    expect(verifiedAge(undefined, NOW)).toEqual({ label: 'unverified', stale: true })
    expect(verifiedAge('2026-10-20T12:00:00Z', NOW)).toEqual({ label: 'verified 24h ago', stale: false })
    expect(verifiedAge('2026-10-01T12:00:00Z', NOW).stale).toBe(true)
  })
  it('titles the pending decisions with their age, and the tasks behind done/total', () => {
    const r = row({ id: 'a', title: 'A', section: 'cleared', pending_decisions: [{ id: 'd1', title: 'Pick the auth model', created: '2026-10-18T12:00:00Z' }, { id: 'd2', title: 'Two' }] })
    expect(themeWaiting(r, NOW)).toEqual(['needs decision: Pick the auth model (3d) +1 more'])
    // Every linked decision settled: the server sends an empty list and nothing is waiting.
    expect(themeWaiting(row({ id: 'z', title: 'Z', section: 'cleared', decision_ids: ['d1', 'd2'], pending_decisions: [] }), NOW)).toEqual([])
    // An older server without the field falls back to the raw count.
    expect(themeWaiting(row({ id: 'y', title: 'Y', section: 'cleared', decision_ids: ['d1'] }), NOW)).toEqual(['1 decision pending'])
    expect(themeCountsText({ available: true, done: 3, total: 11, counts: { done: 3, doing: 2, review: 1, todo: 5, cancelled: 4 }, missing: 2 })).toBe('2 doing · 1 review · 5 todo · 2 linked tasks not found')
    expect(themeCountsText({ available: true, total: 0 })).toBe('')
  })
  it('names the people and what is waiting', () => {
    const r = row({ id: 'a', title: 'A', section: 'cleared', accountable: 'chrispian', executing_owner: 'nanite-team', decision_ids: ['d1', 'd2'], unmet_dependencies: 1 })
    expect(themeOwners(r)).toBe('chrispian · nanite-team')
    expect(themeWaiting(r)).toEqual(['2 decisions pending', 'waits on 1 theme'])
  })
})

describe('themeRowActions', () => {
  it('offers moves only when the server numbered the sequence, and the lifecycle actions where a row sits', () => {
    expect(themeRowActions('cleared', undefined, 3).map((a) => a.action)).toEqual(['park', 'finish'])
    expect(themeRowActions('cleared', 2, 3).map((a) => [a.action, a.position])).toEqual([['resequence', 1], ['resequence', 3], ['park', undefined], ['finish', undefined]])
    expect(themeRowActions('queued', undefined, 3).map((a) => a.action)).toEqual(['approve', 'park', 'archive'])
    expect(themeRowActions('landed', 1, 3)).toEqual([])
  })
})

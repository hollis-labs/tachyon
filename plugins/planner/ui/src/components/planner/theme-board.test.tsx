import { cleanup, fireEvent, render, screen, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { ThemeBoardView } from '@planner/components/planner/theme-board'
import { themeCountsText, themeOwners, themeProgressFraction, themeProgressText, themeWaiting, verifiedAge } from '@planner/lib/planner'
import type { ThemeBoard, ThemeRow, ThemeSectionKey } from '@planner/api/types'

afterEach(cleanup)

const NOW = Date.parse('2026-10-21T12:00:00Z')
const row = (over: Partial<ThemeRow> & { id: string; title: string; section: ThemeSectionKey }): ThemeRow => ({
  source: 'theme', db: 'themes', rev: 3, status: 'approved', updated: '2026-10-21T10:00:00Z', blocked_count: 0, unmet_dependencies: 0,
  progress: { available: true, done: 12, total: 40, open: 20, linked: true }, ...over,
})
const board = (sections: Partial<Record<ThemeSectionKey, ThemeRow[]>>): ThemeBoard => ({
  scope: 'themes', generated_at: '2026-10-21T12:00:00Z', recent_hours: 336, limit: 50, notices: [],
  sections: { in_flight: [], cleared: [], queued: [], parked: [], landed: [], proposed: [], ...sections },
  counts: { in_flight: 0, cleared: 0, queued: 0, parked: 0, landed: 0, proposed: 0 },
  totals: { in_flight: 0, cleared: 0, queued: 0, parked: 0, landed: 0, proposed: 0 },
  progress: { done: 12, total: 40 },
})
const view = (b: ThemeBoard, props = {}) => render(<MemoryRouter><ThemeBoardView board={b} nowMs={NOW} {...props} /></MemoryRouter>)

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
    expect(themeCountsText({ available: true, done: 3, total: 11, counts: { done: 3, doing: 2, review: 1, todo: 5, cancelled: 4 }, missing: 2 })).toBe('2 doing · 1 review · 5 todo · 2 linked tasks not found')
    expect(themeCountsText({ available: true, total: 0 })).toBe('')
  })
  it('names the people and what is waiting', () => {
    const r = row({ id: 'a', title: 'A', section: 'cleared', accountable: 'chrispian', executing_owner: 'nanite-team', decision_ids: ['d1', 'd2'], unmet_dependencies: 1 })
    expect(themeOwners(r)).toBe('chrispian · nanite-team')
    expect(themeWaiting(r)).toEqual(['2 decisions pending', 'waits on 1 theme'])
  })
})

describe('ThemeBoardView', () => {
  it('shows each section with its themes in the order given and their progress', () => {
    view(board({
      in_flight: [row({ id: 'b-1', title: 'Managed agent capacity', section: 'in_flight', last_verified: '2026-10-21T09:00:00Z', accountable: 'chrispian', blocked_count: 2, first_blocker: 'waiting on auth', next_step: 'ship slice 1' })],
      cleared: [row({ id: 'b-2', title: 'Nanite as Tether provider', section: 'cleared' }), row({ id: 'b-3', title: 'Second in line', section: 'cleared' })],
      queued: [row({ id: 'b-4', title: 'Planner MCP', section: 'queued', status: 'ready', progress: { available: true, total: 0, linked: false, note: 'no tasks linked' } })],
      parked: [row({ id: 'b-5', title: 'Mac companion', section: 'parked', status: 'paused', idle_tasks: 4 })],
    }))
    const cleared = within(screen.getByRole('region', { name: 'Cleared' }))
    const titles = cleared.getAllByRole('link').map((l) => l.getAttribute('aria-label'))
    expect(titles).toEqual(['CLEARED Nanite as Tether provider, position 1', 'CLEARED Second in line, position 2'])
    const inFlight = within(screen.getByRole('region', { name: 'In flight' }))
    expect(inFlight.getByRole('progressbar', { name: 'Managed agent capacity progress' }).getAttribute('aria-valuenow')).toBe('30')
    expect(inFlight.getByText('12/40 done')).toBeTruthy()
    expect(inFlight.getByText(/Blocked \(2\): waiting on auth · Next: ship slice 1/)).toBeTruthy()
    // Nothing linked says so, with no bar.
    const queued = within(screen.getByRole('region', { name: 'Queued' }))
    expect(queued.getByText('no tasks linked')).toBeTruthy()
    expect(queued.queryByRole('progressbar')).toBeNull()
    expect(within(screen.getByRole('region', { name: 'Parked' })).getByText(/4 tasks idle/)).toBeTruthy()
  })

  it('hides proposed themes until asked, and says when a section is empty', () => {
    const b = board({ proposed: [row({ id: 'p-1', title: 'A rough idea', section: 'proposed', status: 'proposed' })] })
    const { unmount } = view(b)
    expect(screen.queryByRole('region', { name: 'Proposed' })).toBeNull()
    expect(screen.getByText('Nothing is parked.')).toBeTruthy()
    unmount()
    view(b, { showProposed: true })
    expect(within(screen.getByRole('region', { name: 'Proposed' })).getByText('A rough idea')).toBeTruthy()
  })

  it('shows notices and an unverified record as stale', () => {
    const b = board({ cleared: [row({ id: 'b-6', title: 'Stale theme', section: 'cleared', progress: { available: false } })] })
    b.notices = ['Task progress is unavailable for some themes; Torque\'s task API did not answer.']
    view(b)
    expect(screen.getByRole('status').textContent).toContain('Task progress is unavailable')
    expect(screen.getByText('progress unavailable')).toBeTruthy()
    expect(screen.getByText(/unverified/)).toBeTruthy()
  })
})

describe('ThemeBoardView actions', () => {
  const two = () => board({
    in_flight: [row({ id: 'a', title: 'First', section: 'in_flight', order: 10 })],
    cleared: [row({ id: 'b', title: 'Second', section: 'cleared', order: 20 })],
    queued: [row({ id: 'c', title: 'Queued one', section: 'queued', status: 'ready' })],
    parked: [row({ id: 'd', title: 'Parked one', section: 'parked', status: 'paused' })],
  })
  it('shows no buttons without a handler', () => {
    view(two())
    expect(screen.queryByRole('button')).toBeNull()
  })
  it('offers the moves that make sense where a row sits', () => {
    view(two(), { onAction: vi.fn() })
    const first = within(screen.getByRole('group', { name: 'Actions for First' }))
    expect(first.queryByText('Up')).toBeNull() // already first
    expect(first.getByText('Down')).toBeTruthy()
    const second = within(screen.getByRole('group', { name: 'Actions for Second' }))
    expect(second.getByText('Up')).toBeTruthy()
    expect(second.queryByText('Down')).toBeNull() // already last
    expect(within(screen.getByRole('group', { name: 'Actions for Queued one' })).getByText('Approve')).toBeTruthy()
    expect(within(screen.getByRole('group', { name: 'Actions for Parked one' })).getByText('Unpark')).toBeTruthy()
  })
  it('sends the row and the target position', () => {
    const onAction = vi.fn().mockResolvedValue(undefined)
    view(two(), { onAction })
    fireEvent.click(within(screen.getByRole('group', { name: 'Actions for Second' })).getByText('Up'))
    expect(onAction).toHaveBeenCalledWith(expect.objectContaining({ id: 'b', rev: 3 }), 'resequence', { position: 1 })
    fireEvent.click(within(screen.getByRole('group', { name: 'Actions for First' })).getByText('Down'))
    expect(onAction).toHaveBeenLastCalledWith(expect.objectContaining({ id: 'a' }), 'resequence', { position: 2 })
    fireEvent.click(within(screen.getByRole('group', { name: 'Actions for Queued one' })).getByText('Approve'))
    expect(onAction).toHaveBeenLastCalledWith(expect.objectContaining({ id: 'c' }), 'approve', undefined)
  })
})

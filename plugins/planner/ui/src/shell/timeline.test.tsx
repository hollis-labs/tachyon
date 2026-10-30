import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { PlannerApiProvider } from '@planner/api/context'
import { PlannerApiError } from '@planner/api/errors'
import type { ThemeTimeline } from '@planner/api/types'
import { ThemeTimelineView, ago, toTimelineEvent, useThemeTimeline } from './timeline'

afterEach(cleanup)

const now = Date.parse('2026-10-30T12:00:00Z')
const page = (over: Partial<ThemeTimeline> = {}): ThemeTimeline => ({
  milestones: [
    { key: 'approved', state: 'reached', at: '2026-10-27T10:00:00Z' },
    { key: 'first_done', state: 'reached', at: '2026-10-28T10:00:00Z' },
    { key: 'half_done', state: 'next' },
  ],
  momentum: { label: 'rising', score: 0.7, text: 'Rising: 3 done and 1 PR merged in 24h.' },
  events: [
    { seq: 412, at: '2026-10-30T11:48:00Z', kind: 'task', text: 'CW-1 moved to Review', refs: [], delta: { done: 0, doing: -1, review: 1, blocked: 0 }, arrow: 'up' },
    { seq: 400, at: '2026-10-29T10:00:00Z', kind: 'blocker', text: 'Blocked: contract review', refs: [], delta: { done: 0, doing: 0, review: 0, blocked: 1 }, arrow: 'down' },
  ],
  next_before_seq: null,
  ...over,
})

function Harness() {
  const t = useThemeTimeline('t1')
  return <ThemeTimelineView state={t} progress={{ done: 3, total: 10 }} />
}
const setup = (fn: ReturnType<typeof vi.fn>) => render(<PlannerApiProvider client={{ provider: 'tachyon', subscribeEvents: () => () => {}, plannerThemeTimeline: fn } as never}><Harness /></PlannerApiProvider>)

describe('timeline', () => {
  it('groups by Today, Yesterday and Earlier, and writes arrows with their text', () => {
    expect(ago('2026-10-30T11:48:00Z', now)).toBe('12m ago')
    expect(ago('2026-10-30T09:00:00Z', now)).toBe('3h ago')
    const e = toTimelineEvent(page().events[0], now)
    expect(e).toMatchObject({ day: 'Today', facet: 'tasks', dot: 'doing', delta: 'doing −1, review +1', momentum: { glyph: '▲', label: 'Raises momentum' } })
    expect(e.time).toMatch(/^\d{2}:\d{2}$/)
    expect(toTimelineEvent(page().events[1], now)).toMatchObject({ day: 'Yesterday', tone: 'blocked', facet: 'blockers', dot: 'blocked' })
    expect(toTimelineEvent({ ...page().events[1], at: '2026-10-20T10:00:00Z' }, now)).toMatchObject({ day: 'Earlier', time: 'Oct 20' })
  })
  it('shows only the milestones the server sends (no v1 tag) and says where history starts', async () => {
    setup(vi.fn().mockResolvedValue(page()))
    const list = await screen.findByRole('list', { name: /Milestones/i })
    expect(list.textContent).toContain('Approved')
    expect(list.textContent).toContain('Half of tasks done')
    expect(list.textContent).toContain('next · 2 to go')
    expect(list.textContent).not.toMatch(/v1/i)
    expect(screen.getByText(/History starts Oct 29/)).toBeTruthy()
  })
  it('pages earlier events with the cursor', async () => {
    const fn = vi.fn()
      .mockResolvedValueOnce(page({ next_before_seq: 400 }))
      .mockResolvedValueOnce(page({ events: [{ seq: 300, at: '2026-10-20T10:00:00Z', kind: 'pr', text: 'PR 160 merged', refs: [], arrow: 'up' }], next_before_seq: null }))
    setup(fn)
    fireEvent.click(await screen.findByRole('button', { name: 'Load earlier' }))
    expect(await screen.findByText(/merged/)).toBeTruthy()
    expect(fn).toHaveBeenLastCalledWith('t1', { before_seq: 400, limit: 30 })
    expect(screen.queryByRole('button', { name: 'Load earlier' })).toBeNull()
  })
  it('says the timeline is unavailable on a backend without the route, and retries a failure', async () => {
    setup(vi.fn().mockRejectedValue(new PlannerApiError(404, 'nope', 'not_found')))
    expect(await screen.findByText(/not available on this backend yet/)).toBeTruthy()
    cleanup()
    const fn = vi.fn().mockRejectedValueOnce(new PlannerApiError(500, 'boom')).mockResolvedValue(page())
    setup(fn)
    fireEvent.click(await screen.findByRole('button', { name: 'Retry' }))
    await waitFor(() => expect(screen.getByText('CW-1 moved to Review')).toBeTruthy())
  })
})

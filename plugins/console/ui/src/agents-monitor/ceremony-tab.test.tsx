import { cleanup, render, screen, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { CeremonyTab, type CeremonyMeter } from './ceremony-tab'

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
})

const NOW = Date.parse('2026-11-14T23:00:00Z')
const meter = (over: Partial<CeremonyMeter> = {}): CeremonyMeter => ({
  checked_at: '2026-11-14T23:00:00Z', window_days: 7,
  gates: { total: 93, reviews: 78, approvals: 0, checkpoints: 15, asks: 0 },
  median_wait_seconds: 2150, wait_samples: 50,
  stalled: [{ id: 'CW-20260919-0017', title: 'Atlas MCP read-only', status: 'blocked', updated_at: '2026-09-19T23:28:12Z' }], stalled_total: 156,
  pending_checkpoints: [{ id: 'c1', task_id: 'CW-20260824-0008', type: 'pr_review', title: 'Merge PR #311', emitted_at: '2026-09-14T18:39:09Z' }], pending_checkpoints_total: 28,
  activity: 56249, done: 92, previous: null, comparison: 'pending',
  sources: { reviews: { ok: true }, approvals: { ok: true }, checkpoint_history: { ok: true }, asks: { ok: true }, stalled: { ok: true }, pending_checkpoints: { ok: true }, done: { ok: true }, activity: { ok: true } },
  ...over,
})
const show = (m: CeremonyMeter) => {
  const f = vi.fn((_url: string) => Promise.resolve(new Response(JSON.stringify(m), { status: 200, headers: { 'Content-Type': 'application/json' } })))
  render(<MemoryRouter><CeremonyTab now={NOW} fetch={f} /></MemoryRouter>)
  return f
}

describe('Ceremony', () => {
  it('shows gates with their breakdown, median wait, stalled count and activity against done', async () => {
    const f = show(meter())
    const gates = await screen.findByText('Gates this week')
    expect(gates.parentElement?.textContent).toContain('93')
    expect(gates.parentElement?.textContent).toContain('78 reviews · 0 approvals · 15 checkpoints · 0 asks')
    expect(screen.getByText('Median wait').parentElement?.textContent).toContain('36m')
    expect(screen.getByText('Stalled tasks').parentElement?.textContent).toContain('156')
    expect(screen.getByText('Activity / done').parentElement?.textContent).toContain('56,249 / 92')
    expect(screen.getByText('week-over-week starts after 7 days of snapshots')).toBeTruthy()
    expect(String(f.mock.calls[0][0])).toContain('/ceremony')
  })

  it('lists the oldest pending checkpoints and stalled tasks with task chips and ages', async () => {
    show(meter())
    const pending = await screen.findByRole('region', { name: 'Oldest pending checkpoints' })
    expect(pending.textContent).toContain('· 28')
    const row = within(pending).getByRole('listitem')
    expect(row.textContent).toContain('pr review')
    expect(row.textContent).toContain('Merge PR #311')
    expect(row.querySelector('a[data-ref-kind="torque.task"]')?.getAttribute('href')).toBe('/tasks/CW-20260824-0008')
    const stalled = screen.getByRole('region', { name: 'Oldest stalled tasks' })
    expect(within(stalled).getByRole('listitem').textContent).toContain('no update for')
  })

  it('flags a regression and says when figures are missing', async () => {
    show(meter({ previous: { day: '2026-11-07', activity: 40000, done: 95, gates: 80 }, comparison: 'regression', sources: { ...meter().sources, done: { ok: false, error: 'torque unreachable' } } }))
    expect((await screen.findByText('Activity rose 25% or more while closed work did not.')).getAttribute('role')).toBe('alert')
    expect(screen.getByText('a week ago: 40,000 / 95')).toBeTruthy()
    expect(screen.getByText(/could not read closed tasks/)).toBeTruthy()
  })
})

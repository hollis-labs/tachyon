import { cleanup, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { PlannerApiProvider } from '@planner/api/context'
import PlannerShell from './PlannerShell'

vi.mock('sonner', () => ({ toast: Object.assign(vi.fn(), { error: vi.fn(), success: vi.fn() }) }))
afterEach(() => { cleanup(); vi.unstubAllGlobals() })

const row = (id: string, rank: number) => ({ source: 'theme', db: 'themes', id, rev: 1, rank, title: `Theme ${id}`, status: 'approved', section: 'in_flight', updated: '2026-10-21T10:00:00Z', blocked_count: 0, unmet_dependencies: 0, progress: { available: true, done: 1, total: 4, open: 3, linked: true, counts: { done: 1 } } })
const board = () => ({
  scope: 'themes', generated_at: '', recent_hours: 24, limit: 50,
  sections: { in_flight: [row('t1', 1), row('t2', 2)], queued: [], proposed: [], landed: [], parked: [], cleared: [] },
  counts: { in_flight: 2, cleared: 0, queued: 0, parked: 0, landed: 0, proposed: 0 }, totals: { in_flight: 2, cleared: 0, queued: 0, parked: 0, landed: 0, proposed: 0 }, notices: [], progress: { done: 0, total: 0 },
})
const api = () => ({
  provider: 'tachyon',
  subscribeEvents: () => () => {},
  plannerThemeBoard: vi.fn(async () => board()),
  plannerGet: vi.fn(async () => ({ comments: [] })),
  plannerSchema: vi.fn(async () => ({ db: 'themes', id: 'x', description: '', required: [], fields: {}, enums: {}, link_kinds: [] })),
  plannerList: vi.fn(async () => []),
  plannerLinks: vi.fn(async () => ({ outbound: [], inbound: [], external: [] })),
  plannerNeedYou: vi.fn(async () => { throw new Error('404') }),
  plannerThemeTasks: vi.fn(async () => { throw new Error('404') }),
  plannerThemeTimeline: vi.fn(async () => { throw new Error('404') }),
})
const Where = () => <p data-testid="where">{useLocation().pathname}</p>
function page() {
  render(
    <PlannerApiProvider client={api() as never}>
      <MemoryRouter initialEntries={['/planner/themes']}>
        <Where />
        <Routes><Route path="/planner/themes/:id?" element={<PlannerShell />} /></Routes>
      </MemoryRouter>
    </PlannerApiProvider>,
  )
}

describe('Planner shell default selection', () => {
  it('opens the first theme on a wide screen so the focus pane is never empty', async () => {
    vi.stubGlobal('matchMedia', () => ({ matches: true, addEventListener() {}, removeEventListener() {} }))
    page()
    await waitFor(() => expect(screen.getByTestId('where').textContent).toBe('/planner/themes/t1'))
  })
  it('keeps the list on a narrow screen', async () => {
    vi.stubGlobal('matchMedia', () => ({ matches: false, addEventListener() {}, removeEventListener() {} }))
    page()
    await screen.findByText('Theme t1')
    expect(screen.getByTestId('where').textContent).toBe('/planner/themes')
  })
})

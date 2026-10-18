import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { afterEach, expect, it, vi } from 'vitest'
import { PlannerApiProvider } from '@planner/api/context'
import { TachyonPlannerClient } from '@planner/api/client'
import PlannerPage from './PlannerPage'

const id = '019a0000-0000-7000-8000-000000000001'
afterEach(cleanup)
it('uses the owned client for board, list, search and opening a UUID detail', async () => {
  const item = { id, title: 'Owned idea', status: 'new', rev: 4 }
  const api = Object.assign(new TachyonPlannerClient(), {
    subscribeEvents: () => () => {},
    plannerDatabases: vi.fn().mockResolvedValue([{ name: 'ideas', count: 1 }]),
    plannerBoard: vi.fn().mockResolvedValue({ scope: 'curated', generated_at: new Date().toISOString(), recent_hours: 24, active_hours: 24, limit: 30, sections: { in_flight: [], landed: [], pre_flight: [], holds: [{ ...item, source: 'inbox', db: 'ideas', updated: '' }] }, counts: {}, totals: {}, notices: [] }),
    plannerList: vi.fn().mockResolvedValue([item]),
    plannerSearch: vi.fn().mockResolvedValue([{ ...item, db: 'ideas' }]),
    plannerGet: vi.fn().mockResolvedValue(item),
    plannerTaskTitles: vi.fn().mockResolvedValue({}),
    plannerSchema: vi.fn().mockResolvedValue({ db: 'ideas', id: 'owned', fields: { title: { type: 'string' } }, required: ['title'], enums: {}, link_kinds: [] }),
  })
  render(<MemoryRouter initialEntries={['/planner']}><PlannerApiProvider client={api}><Routes><Route path="/planner/:db?/:id?" element={<PlannerPage />} /></Routes></PlannerApiProvider></MemoryRouter>)
  await waitFor(() => expect(api.plannerBoard).toHaveBeenCalledWith({ scope: 'curated' }))
  fireEvent.click(screen.getByRole('tab', { name: /Ideas/ }))
  await waitFor(() => expect(api.plannerList).toHaveBeenCalledWith('ideas'))
  await screen.findByText('Owned idea')
  const search = screen.getByLabelText('Search the planner')
  fireEvent.change(search, { target: { value: 'Owned' } })
  await waitFor(() => expect(api.plannerSearch).toHaveBeenCalledWith('Owned', undefined, expect.any(AbortSignal)))
  fireEvent.click(await screen.findByRole('button', { name: `Open ${id}: Owned idea` }))
  await screen.findByRole('dialog')
  await waitFor(() => expect(api.plannerGet).toHaveBeenCalledWith('ideas', id))
})

/** @vitest-environment jsdom */
import { afterEach, expect, it, vi } from 'vitest'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter, useLocation } from 'react-router-dom'
import type { Epic, PlanDetail, Project, Sprint } from '@tachyon/torque-client'
import { createDefaultRefRegistry } from '../refs'
import { AutolinkProvider, Linkified } from '.'
import { createLiveResolvers } from './live'

afterEach(cleanup)

const at = '2026-10-23T00:00:00Z'
const api = {
  plannerTaskTitles: vi.fn(async () => ({})),
  plannerResolve: vi.fn(),
  getEpic: vi.fn(async (id: string) => {
    if (id === 'EP-20261023-0404') throw Object.assign(new Error('nf'), { status: 404 })
    return { id, name: 'Approve and run', description: 'Run approved actions.', status: 'active', priority: 2, project_id: 'PRJ-20260917-0001', created_at: at, updated_at: at } as Epic
  }),
  getProject: vi.fn(async (id: string) => ({ id, name: 'Tachyon', description: 'The console.', repo_path: '/repo/tachyon', status: 'active', created_at: at, updated_at: at }) as unknown as Project),
  getSprint: vi.fn(async (id: string) => ({ id, name: 'Sprint 12', goal: 'Ship previews.', status: 'active', approval_mode: 'manual', project_id: 'PRJ-20260917-0001', started_at: at, ended_at: null, created_at: at, updated_at: at }) as Sprint),
  getPlan: vi.fn(async (id: string) => ({
    task: { id, title: 'Roll out cards', status: 'in_progress', updated_at: at },
    plan: { version: 1, phases: [{ id: 'p1', name: 'Build', order: 1, acceptance: 'tests pass' }] },
    progress: { total_children: 4, done: 1, blocked: 0, by_phase: { p1: { total: 4, done: 1, blocked: 0 } } },
  }) as unknown as PlanDetail),
}

function Where() { return <span data-testid="where">{useLocation().pathname}</span> }
function setup(text: string, resolvers = createLiveResolvers(api as never)) {
  return render(<MemoryRouter><AutolinkProvider registry={createDefaultRefRegistry()} resolvers={resolvers}><Linkified text={text} /></AutolinkProvider><Where /></MemoryRouter>)
}
const chipFor = async () => {
  const chip = document.querySelector('[data-ref-kind]') as HTMLElement
  fireEvent.pointerEnter(chip)
  await waitFor(() => expect(chip.getAttribute('data-ref-state')).toBe('found'))
  return chip
}

it.each([
  ['EP-20261023-0002', 'torque.epic', ['Approve and run', 'Run approved actions.', 'P2']],
  ['PRJ-20260917-0001', 'torque.project', ['Tachyon', 'The console.', '/repo/tachyon']],
  ['SP-20261023-0003', 'torque.sprint', ['Sprint 12', 'Ship previews.', 'manual']],
  ['PL-20261023-0004', 'torque.plan', ['Roll out cards', '1. Build (1/4 done): tests pass', '1/4 done']],
])('clicking %s opens the overlay with the object contents instead of leaving the page', async (id, kind, shown) => {
  setup(`see ${id}`)
  const chip = await waitFor(() => { const c = document.querySelector(`[data-ref-kind="${kind}"]`); if (!c) throw new Error('chip'); return c as HTMLElement })
  expect(chip.getAttribute('data-ref-state')).not.toBe('found') // nothing is fetched on mount
  fireEvent.pointerEnter(chip)
  await waitFor(() => expect(chip.getAttribute('data-ref-state')).toBe('found'))
  fireEvent.click(chip)
  const drawer = await screen.findByTestId('ref-drawer')
  for (const text of shown) expect(drawer.textContent).toContain(text)
  expect(screen.getByTestId('where').textContent).toBe('/')
})
it('an unknown container is a not-found chip, and a failing read is retryable rather than unsupported', async () => {
  setup('EP-20261023-0404')
  fireEvent.pointerEnter(document.querySelector('[data-ref-kind]') as HTMLElement)
  await waitFor(() => expect(document.querySelector('[data-ref-kind]')?.getAttribute('data-ref-state')).toBe('not-found'))
  cleanup()
  const failing = createLiveResolvers({ ...api, getSprint: vi.fn(async () => { throw new Error('network') }) } as never)
  setup('SP-20261023-0003', failing)
  fireEvent.pointerEnter(document.querySelector('[data-ref-kind]') as HTMLElement)
  await waitFor(() => expect(document.querySelector('[data-ref-kind]')?.getAttribute('data-ref-state')).toBe('error'))
})
it('without container reads in the client the chips keep navigating to their page', async () => {
  const { getEpic: _e, getProject: _p, getSprint: _s, getPlan: _l, ...partial } = api
  setup('EP-20261023-0002', createLiveResolvers(partial as never))
  fireEvent.click(document.querySelector('[data-ref-kind]') as HTMLElement)
  expect(screen.getByTestId('where').textContent).toBe('/epics/EP-20261023-0002')
  expect(screen.queryByTestId('ref-drawer')).toBeNull()
})
it('a click without any prior hover (touch, keyboard) still opens the overlay with content', async () => {
  setup('EP-20261023-0002')
  fireEvent.click(document.querySelector('[data-ref-kind]') as HTMLElement)
  const drawer = await screen.findByTestId('ref-drawer')
  await waitFor(() => expect(drawer.textContent).toContain('Run approved actions.'))
})

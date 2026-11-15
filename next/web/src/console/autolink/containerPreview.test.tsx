/** @vitest-environment jsdom */
import { afterEach, expect, it, vi } from 'vitest'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter, useLocation } from 'react-router-dom'
import type { Epic, PlanDetail, Project, Sprint } from '@tachyon/torque-client'
import { createDefaultRefRegistry } from '../refs'
import { AutolinkProvider, Linkified } from '.'
import { createLiveResolvers } from './live'

afterEach(() => { cleanup(); paths.length = 0; respond = () => undefined })

const at = '2026-10-23T00:00:00Z'
const bodies: Record<string, (id: string) => unknown> = {
  epics: (id) => ({ id, name: 'Approve and run', status: 'active', priority: 2, project_id: 'PRJ-20260917-0001', created_at: at, updated_at: at }),
  projects: (id) => ({ id, name: 'Tachyon', status: 'active', created_at: at, updated_at: at }),
  sprints: (id) => ({ id, name: 'Sprint 12', goal: 'Ship previews.', status: 'active', approval_mode: 'manual', project_id: 'PRJ-20260917-0001', started_at: at, created_at: at, updated_at: at }),
  plans: (id) => ({ task: { id, title: 'Roll out cards', status: 'doing', updated_at: at }, plan: { phases: [{ id: 'p1', name: 'Build', order: 1 }] },
    progress: { total_children: 4, done: 1, blocked: 0, by_phase: { p1: { total: 4, done: 1, blocked: 0 } } } }),
}
const api = { plannerTaskTitles: vi.fn(async () => ({})), plannerResolve: vi.fn() }
const paths: string[] = []
let respond: (path: string) => Response | undefined = () => undefined
const get = vi.fn(async (path: string) => {
  paths.push(path)
  const forced = respond(path)
  if (forced) return forced
  const m = /preview\/(\w+)\/([^/]+)$/.exec(path)!
  if (m[2] === 'EP-20261023-0404') return new Response('{}', { status: 404 })
  return new Response(JSON.stringify(bodies[m[1]](m[2])), { status: 200 })
})

function Where() { return <span data-testid="where">{useLocation().pathname}</span> }
function setup(text: string, resolvers = createLiveResolvers(api as never, undefined, get)) {
  return render(<MemoryRouter><AutolinkProvider registry={createDefaultRefRegistry()} resolvers={resolvers}><Linkified text={text} /></AutolinkProvider><Where /></MemoryRouter>)
}
const chipFor = async () => {
  const chip = document.querySelector('[data-ref-kind]') as HTMLElement
  fireEvent.pointerEnter(chip)
  await waitFor(() => expect(chip.getAttribute('data-ref-state')).toBe('found'))
  return chip
}

it.each([
  ['EP-20261023-0002', 'torque.epic', ['Approve and run', 'P2', 'active']],
  ['PRJ-20260917-0001', 'torque.project', ['Tachyon', 'active']],
  ['SP-20261023-0003', 'torque.sprint', ['Sprint 12', 'Ship previews.', 'manual']],
  ['PL-20261023-0004', 'torque.plan', ['Roll out cards', '1. Build (1/4 done)', '1/4 done']],
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
it('reads only the operator-gated preview routes, never the raw Torque passthrough', async () => {
  setup('see EP-20261023-0002')
  fireEvent.pointerEnter(document.querySelector('[data-ref-kind]') as HTMLElement)
  await waitFor(() => expect(paths.length).toBeGreaterThan(0))
  expect(paths).toEqual(['/api/plugins/console/v1/preview/epics/EP-20261023-0002'])
})
it('an unknown container is a not-found chip, a failing read is retryable, and a missing elevation is an error state', async () => {
  setup('EP-20261023-0404')
  fireEvent.pointerEnter(document.querySelector('[data-ref-kind]') as HTMLElement)
  await waitFor(() => expect(document.querySelector('[data-ref-kind]')?.getAttribute('data-ref-state')).toBe('not-found'))
  for (const status of [500, 403, 401]) {
    cleanup()
    respond = () => new Response('{}', { status })
    setup('SP-20261023-0003')
    fireEvent.pointerEnter(document.querySelector('[data-ref-kind]') as HTMLElement)
    await waitFor(() => expect(document.querySelector('[data-ref-kind]')?.getAttribute('data-ref-state')).toBe('error'))
  }
  respond = () => undefined
})
it('a click without any prior hover (touch, keyboard) still opens the overlay with content', async () => {
  setup('EP-20261023-0002')
  fireEvent.click(document.querySelector('[data-ref-kind]') as HTMLElement)
  const drawer = await screen.findByTestId('ref-drawer')
  await waitFor(() => expect(drawer.textContent).toContain('Approve and run'))
})

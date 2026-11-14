import { render, screen } from '@testing-library/react'
import { expect, it, vi } from 'vitest'
import { PlannerBoardTab, PlannerNeedTab, PLANNER_AGENTS } from './PlannerPanel'
import { panelsFor } from './registry'

const reply = (map: Record<string, unknown>) => vi.stubGlobal('fetch', vi.fn(async (u: string) => {
  const k = Object.keys(map).find((p) => u.includes(p))
  return k ? new Response(JSON.stringify(map[k]), { status: 200 }) : new Response('{}', { status: 404 })
}))

it('is registered for the planner agent only', () => {
  expect(panelsFor({ agentId: PLANNER_AGENTS[0] }).map((p) => p.id)).toContain('planner')
  expect(panelsFor({ agentId: 'someone-else' }).map((p) => p.id)).not.toContain('planner')
})

it('Board lists in-flight themes from the live board with progress', async () => {
  reply({ '/board': { sections: { in_flight: [{ id: 'a', title: 'Ship planner', status: 'approved', blocked_count: 0, next_step: 'Review', progress: { available: true, done: 2, total: 4, counts: { doing: 1 } } }] }, counts: {}, progress: { done: 2, total: 4 } } })
  render(<PlannerBoardTab />)
  expect(await screen.findByText('Ship planner')).toBeTruthy()
  expect(screen.getByText('Next: Review')).toBeTruthy()
})

it('Need you shows waiting decisions and surfaces a 403 as an operator-access message', async () => {
  reply({ '/need-you': { total: 3, by_reason: { decision_waiting: 3 }, items: [] }, '/decisions': [{ id: 'd1', title: 'Pick a runtime' }] })
  const { unmount } = render(<PlannerNeedTab />)
  expect(await screen.findByText('Pick a runtime')).toBeTruthy()
  unmount()
  vi.stubGlobal('fetch', vi.fn(async () => new Response('{}', { status: 403 })))
  render(<PlannerNeedTab />)
  expect((await screen.findAllByRole('alert'))[0].textContent).toMatch(/operator access/)
})

import { describe, expect, it, vi } from 'vitest'
import type { ConfirmResult } from '@console/seams/identity-confirm'
import { AgentsHttpError, createHttpAgentsClient } from './http-client'

const json = (status: number, body: unknown) => new Response(body === undefined ? '' : JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })

function setup(res: Response | (() => Response)) {
  const f = vi.fn(async (_url: string, _init?: RequestInit) => (typeof res === 'function' ? res() : res))
  return { f, c: createHttpAgentsClient('/api/plugins/console/v1', f as unknown as typeof fetch) }
}

describe('createHttpAgentsClient', () => {
  it('lists agents with query params under the console plugin base', async () => {
    const { f, c } = setup(json(200, []))
    expect(await c.listAgents({ search: 'cap', provider: 'opencode' })).toEqual([])
    expect(f.mock.calls[0][0]).toBe('/api/plugins/console/v1/agents?search=cap&provider=opencode')
    expect(f.mock.calls[0][1]?.method).toBe('GET')
  })

  it('maps 404 to null for single-record reads', async () => {
    const { c } = setup(() => json(404, { error: 'not found' }))
    expect(await c.getAgent('nope')).toBeNull()
    expect(await c.getPlan('p1')).toBeNull()
    expect(await c.getTeam('t1')).toBeNull()
  })

  it('rejects with the server error message otherwise', async () => {
    const { c } = setup(json(502, { error: 'tether unavailable' }))
    await expect(c.listAgents()).rejects.toThrow('tether unavailable')
    const { c: c2 } = setup(new Response('<html>', { status: 404 }))
    await expect(c2.listTeams()).rejects.toBeInstanceOf(AgentsHttpError)
  })

  it('posts plans and step-up confirmations to the contract routes', async () => {
    const { f, c } = setup(() => json(200, { id: 'p1' }))
    const confirmation = { outcome: 'granted', token: 'tok' } as unknown as ConfirmResult
    await c.planChange({ agentId: 'a/b', draft: {} as never, baseRevision: 'r1', note: 'n' })
    await c.approvePlan('p1', confirmation)
    await c.applyPlan('p1', confirmation)
    await c.rollbackApply('p1', confirmation)
    await c.listAssignments({ agentId: 'a1', targetId: 'WS-1' })
    const calls = f.mock.calls.map(([u, i]) => `${i?.method} ${u}`)
    expect(calls).toEqual([
      'POST /api/plugins/console/v1/agents/a%2Fb/plans',
      'POST /api/plugins/console/v1/agent-plans/p1/approve',
      'POST /api/plugins/console/v1/agent-plans/p1/apply',
      'POST /api/plugins/console/v1/agent-plans/p1/rollback',
      'GET /api/plugins/console/v1/assignments?agent_id=a1&target_id=WS-1',
    ])
    expect(JSON.parse(String(f.mock.calls[0][1]?.body))).toEqual({ draft: {}, baseRevision: 'r1', note: 'n' })
    expect(JSON.parse(String(f.mock.calls[1][1]?.body))).toEqual({ confirmation })
  })
})

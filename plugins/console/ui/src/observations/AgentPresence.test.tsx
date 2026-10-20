/** @vitest-environment jsdom */
import { afterEach, expect, it, vi } from 'vitest'
import { act, cleanup, render, screen } from '@testing-library/react'
import { AgentPresence } from './AgentPresence'
import { monitorForSession } from './model'
import { parseFleetAgents } from '../fleet/lib/fleet-agents'

const mocks = vi.hoisted(() => ({ json: vi.fn(), activity: vi.fn(), observed: { events: [], checkedAt: 0, error: null, limited: false } }))
vi.mock('./store', () => ({ observationJSON: mocks.json, useObservations: () => mocks.observed }))
vi.mock('../client', () => ({ consoleClient: { getActivityAgents: mocks.activity } }))
afterEach(() => { cleanup(); vi.useRealTimers(); vi.resetAllMocks() })

it('polls native activity by exact session identity, ages failed observations, and stops on unmount', async () => {
  vi.useFakeTimers()
  const now = Date.parse('2026-10-20T23:00:00Z'); vi.setSystemTime(now)
  mocks.json.mockResolvedValue({ alive: true, live_state: 'busy', last_activity_at: new Date(now).toISOString() })
  mocks.activity.mockResolvedValue({ agents: [{ id: 'native', session_id: 'native', state: 'busy', cwd: '/tmp/.tether-worker/workspaces/s1/workroot', last_action: 'Editing a file', last_action_at: new Date(now).toISOString() }] })
  let view: ReturnType<typeof render>
  await act(async () => { view = render(<AgentPresence sessionId="s1" />) })
  expect(screen.getByLabelText('Agent activity').textContent).toContain('Editing a file')
  mocks.json.mockRejectedValue(new Error('offline')); mocks.activity.mockRejectedValue(new Error('offline'))
  await act(async () => { await vi.advanceTimersByTimeAsync(5000) })
  expect(screen.getByLabelText('Agent activity').textContent).not.toContain('Editing a file')
  expect(screen.getByLabelText('Agent activity').textContent).toContain('Last seen')
  view!.unmount()
  const calls = mocks.json.mock.calls.length
  await act(async () => { await vi.advanceTimersByTimeAsync(10_000) })
  expect(mocks.json.mock.calls.length).toBe(calls)
})
it('refreshes a durable binding when an agent moves to a new session', async () => {
  vi.useFakeTimers()
  let session = 's1'
  mocks.json.mockImplementation(async (path: string) => path.startsWith('/binding') ? { SessionID: session } : { alive: true, live_state: 'idle' })
  mocks.activity.mockResolvedValue({ agents: [] })
  await act(async () => { render(<AgentPresence address="msg://agent/agent-mux/a1" />) })
  expect(mocks.json).toHaveBeenCalledWith('/sessions/s1/health')
  session = 's2'
  await act(async () => { await vi.advanceTimersByTimeAsync(5000) })
  expect(mocks.json).toHaveBeenCalledWith('/sessions/s2/health')
  expect(screen.getByLabelText('Agent activity').textContent).toContain('Online · idle')
})
it('never matches an agent by display name or an ambiguous managed workspace', () => {
  const agents = parseFleetAgents({ agents: [{ id: 'x', session_id: 's2', last_action: 'Editing a file', cwd: '/tmp/shared' }] })
  expect(monitorForSession(agents, 's1')).toBeUndefined()
  expect(monitorForSession([...agents, ...agents], 's2')).toBeUndefined()
})

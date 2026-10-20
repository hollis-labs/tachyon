import { afterEach, expect, it, vi } from 'vitest'
import { launchAgent, type LaunchAttempt } from './launch-client'
import { mergeTetherSessions } from './tether-sessions'
import { mergeChatAgents } from '../chat/fleet'
vi.mock('../operator/elevation', () => ({ operatorFetch: (...args: Parameters<typeof fetch>) => fetch(...args) }))
afterEach(() => vi.unstubAllGlobals())
const response = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status })
const attempt = (): LaunchAttempt => ({ input: { launch: 'review-code', worker: 'worker-1', prompt_append: 'Review this change', idempotency_key: 'stable-key' } })

it('creates once, checks state, then starts the exact session with the selected placement and prompt', async () => {
  const fetcher = vi.fn().mockResolvedValueOnce(response({ id: 'session-1' })).mockResolvedValueOnce(response({ state: 'created' })).mockResolvedValueOnce(response({ id: 'session-1' }))
  vi.stubGlobal('fetch', fetcher)
  const a = attempt()
  expect(await launchAgent(a)).toBe('session-1')
  expect(JSON.parse(fetcher.mock.calls[0][1].body)).toEqual(a.input)
  expect(fetcher.mock.calls.map(c => [c[0], c[1]?.method ?? 'GET'])).toEqual([
    ['/api/plugins/console/v1/tether/sessions', 'POST'],
    ['/api/plugins/console/v1/tether/sessions/session-1', 'GET'],
    ['/api/plugins/console/v1/tether/sessions/session-1/launch', 'POST'],
  ])
})
it('reconciles a lost start response without issuing another launch', async () => {
  const fetcher = vi.fn().mockResolvedValueOnce(response({ id: 'session-1' })).mockResolvedValueOnce(response({ state: 'created' })).mockRejectedValueOnce(new Error('Connection lost')).mockResolvedValueOnce(response({ state: 'running' }))
  vi.stubGlobal('fetch', fetcher)
  expect(await launchAgent(attempt())).toBe('session-1')
  expect(fetcher.mock.calls.filter(c => c[0].endsWith('/launch'))).toHaveLength(1)
})
it('retries the same created session after a start refusal', async () => {
  const fetcher = vi.fn().mockResolvedValueOnce(response({ id: 'session-1' })).mockResolvedValueOnce(response({ state: 'created' })).mockResolvedValueOnce(response({ error: { message: 'Worker offline' } }, 502)).mockResolvedValueOnce(response({ state: 'created' }))
    .mockResolvedValueOnce(response({ state: 'created' })).mockResolvedValueOnce(response({ id: 'session-1' }))
  vi.stubGlobal('fetch', fetcher)
  const a = attempt()
  await expect(launchAgent(a)).rejects.toThrow('Worker offline')
  expect(await launchAgent(a)).toBe('session-1')
  expect(fetcher.mock.calls.filter(c => c[0].endsWith('/sessions'))).toHaveLength(1)
})
it('keeps the same create idempotency key when creation response is lost', async () => {
  const fetcher = vi.fn().mockRejectedValueOnce(new Error('Connection lost')).mockResolvedValueOnce(response({ id: 'session-1', replayed: true })).mockResolvedValueOnce(response({ state: 'running' }))
  vi.stubGlobal('fetch', fetcher)
  const a = attempt()
  await expect(launchAgent(a)).rejects.toThrow('Connection lost')
  await launchAgent(a)
  expect(fetcher.mock.calls[0][1].body).toBe(fetcher.mock.calls[1][1].body)
  expect(fetcher.mock.calls.some(c => c[0].endsWith('/launch'))).toBe(false)
})
it('does not restart an ended session', async () => {
  const fetcher = vi.fn().mockResolvedValue(response({ state: 'exited' }))
  vi.stubGlobal('fetch', fetcher)
  await expect(launchAgent({ ...attempt(), sessionId: 'session-1' })).rejects.toThrow('exited')
  expect(fetcher).toHaveBeenCalledTimes(1)
})
const session = { id: 's1', launch_id: 'review-code', provider_id: 'codex', workspace: '', state: 'running', created_at: '2026-10-20T00:00:00Z' }
it('adds remote non-chat sessions without inventing busy state, and excludes ended sessions', () => {
  const rows = mergeTetherSessions([], [session, { ...session, id: 'ended', state: 'exited' }])
  expect(rows.map(a => a.id)).toEqual(['tether:s1'])
  expect(rows[0].state).toBe('unknown')
  expect(rows[0].lastAction?.text).toContain('Running')
})
it('merges exact chat session identity but retains multiple sessions from the same profile', () => {
  const chats = mergeChatAgents([], [{ launch_id: 'review-code', agent_id: 'reviewer', display_name: 'Reviewer', channel: 'chat', session: { id: 's1', state: 'running', last_activity_at: '', at_turn_boundary: true } }])
  expect(mergeTetherSessions(chats, [session, { ...session, id: 's2' }]).map(a => a.sessionId)).toEqual(['s1', 's2'])
})
it('does not collapse distinct sessions sharing one project directory', () => {
  const rows = mergeTetherSessions([], [{ ...session, workspace: '/project' }, { ...session, id: 's2', workspace: '/project' }])
  expect(rows.map(a => a.sessionId)).toEqual(['s1', 's2'])
})

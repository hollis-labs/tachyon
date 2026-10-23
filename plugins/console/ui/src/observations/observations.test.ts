import { afterEach, expect, it, vi } from 'vitest'
import { createObservationStore, eventFromRow, type SessionSource } from './store'
import { activityText, presence, toolLabel, withObservedActivity } from './model'
import { parseFleetAgents } from '../fleet/lib/fleet-agents'
import { deliveryEvidence, deliveryFromTrace } from './delivery'
import { deliveryFromEnvelope } from '../messaging/adapter'

const now = Date.parse('2026-10-20T23:00:00Z')
const row = (seq: number, kind = 'tool.call.started', payload = { tool: 'apply_patch' } as Record<string, unknown>) => ({ seq, kind, at: new Date(now).toISOString(), session_id: 's1', payload_json: JSON.stringify(payload) })
afterEach(() => vi.useRealTimers())

it('keeps exact session identity, tool details and stable ids while dropping heartbeats', () => {
  const event = eventFromRow(row(1))!
  expect(event.sessionId).toBe('s1')
  expect(event.id).toBe('tether:1')
  expect(activityText(event)).toBe('Editing a file')
  expect(toolLabel('tether_message_mark_read')).toBe('Checking messages')
  expect(eventFromRow(row(2, 'session.state_changed', { from: 'running', to: 'running' }))).toBeNull()
  expect(eventFromRow({ ...row(3), at: 'bad' })).toBeNull()
})
it('expires observations and does not present recent tools as current work when runtime is idle', () => {
  const event = eventFromRow(row(1))!
  expect(presence(null, event, now, now, false).tone).toBe('active')
  expect(presence(null, event, now + 30_000, now, false).text).toBe('Last seen')
  expect(presence({ alive: true, live_state: 'idle' }, event, now, now, false).text).toBe('Online · idle')
  expect(presence({ alive: true }, event, now, now, true).tone).toBe('quiet')
  expect(presence({ pending_approval: true }, event, now, now, false).text).toBe('Waiting for approval')
})
it('coalesces refreshes, drains incremental pages, dedupes, and preserves last observations on failure', async () => {
  vi.useFakeTimers(); vi.setSystemTime(now)
  const fetch = vi.fn().mockResolvedValueOnce({ events: [row(1)], next_cursor: 0 })
    .mockResolvedValueOnce({ events: [row(4)], next_cursor: 4 })
    .mockResolvedValueOnce({ events: [row(3), row(2), row(2)], next_cursor: 0 })
    .mockRejectedValueOnce(new Error('offline'))
  const store = createObservationStore(fetch)
  const pending = store.refresh()
  expect(store.refresh()).toBe(pending)
  await pending
  await store.refresh()
  expect(fetch.mock.calls[1][0]).toContain('since_seq=1')
  expect(fetch.mock.calls[2][0]).toContain('cursor=4')
  expect(store.getSnapshot().events.map(e => e.id)).toEqual(['tether:4', 'tether:3', 'tether:2', 'tether:1'])
  await store.refresh()
  expect(store.getSnapshot().error).toBe('offline')
  expect(store.getSnapshot().events[0].id).toBe('tether:4')
})
it('bounds a busy replay and recovers from a reset sequence with a periodic snapshot', async () => {
  vi.useFakeTimers(); vi.setSystemTime(now)
  const fetch = vi.fn().mockResolvedValueOnce({ events: [row(100)] })
    .mockResolvedValueOnce({ events: [row(400)], next_cursor: 400 })
    .mockResolvedValueOnce({ events: [row(300)], next_cursor: 300 })
    .mockResolvedValueOnce({ events: [row(200)], next_cursor: 200 })
    .mockResolvedValueOnce({ events: [row(1)] })
  const store = createObservationStore(fetch)
  await store.refresh(); await store.refresh()
  expect(store.getSnapshot().limited).toBe(true)
  vi.setSystemTime(now + 61_000)
  await store.refresh()
  expect(fetch.mock.calls[4][0]).not.toContain('since_seq')
  expect(store.getSnapshot().events.map(e => e.id)).toEqual(['tether:1'])
})
it('does not equate host acceptance, another message, or an unrelated reply with delivery/read', () => {
  expect(deliveryFromTrace('m1', { message_id: 'm1', attempts: [{ stage: 'host_accepted' }] }, 'queued')).toBe('queued')
  expect(deliveryFromTrace('m1', { message_id: 'm1', attempts: [{ stage: 'turn_submitted' }] }, 'queued')).toBe('delivered')
  expect(deliveryFromTrace('m1', { message_id: 'other', attempts: [{ stage: 'consumed' }] }, 'queued')).toBe('queued')
  expect(deliveryFromTrace('m1', { message_id: 'm1', status: 'dead_letter' }, 'queued')).toBe('failed')
  const env = { delivered_at: null, consumed_at: null, id: 'm1', kind: 'notice' as const, from: 'x', to: 'y', created_at: new Date(now).toISOString() }
  expect(deliveryFromEnvelope(env, [{ ...env, id: 'm2', from: 'y', created_at: new Date(now + 1000).toISOString() }])).toBe('queued')
  expect(deliveryFromEnvelope(env, [{ ...env, id: 'm2', from: 'z', in_reply_to: 'm1' }])).toBe('queued')
  expect(deliveryFromEnvelope(env, [{ ...env, id: 'm2', from: 'y', in_reply_to: 'm1' }])).toBe('replied')
  const events = [eventFromRow(row(1, 'message.delivered', { message_id: 'm1' }))!]
  expect(deliveryEvidence(events, 'm1', 'queued').state).toBe('delivered')
  expect(deliveryEvidence(events, 'm2', 'queued').state).toBe('queued')
  expect(deliveryEvidence(events, 'm1', 'read').state).toBe('read')
})

const sessionRow = (seq: number, session: string, kind: string, payload: Record<string, unknown> = {}) => ({ ...row(seq, kind, payload), session_id: session })
function source(perSession: Record<string, ReturnType<typeof sessionRow>[]>): SessionSource & { queries: string[] } {
  const queries: string[] = []
  return { queries, running: async () => Object.keys(perSession),
    page: async query => {
      queries.push(query)
      const p = new URLSearchParams(query)
      const rows = perSession[p.get('session_id')!] ?? []
      const kind = p.get('kind')
      return { events: rows.filter(r => !kind || r.kind === kind).slice(-Number(p.get('limit') || 200)).reverse() }
    } }
}
it('a flood of chat events cannot hide a quiet worker: each running session is read on its own', async () => {
  vi.useFakeTimers(); vi.setSystemTime(now)
  const flood = Array.from({ length: 200 }, (_, i) => sessionRow(1000 + i, 'chat1', 'tool.call.started', { tool: 'Bash' }))
  const src = source({ chat1: flood, worker1: [sessionRow(3, 'worker1', 'session.work_state_changed', { from: 'idle', to: 'processing' })] })
  const store = createObservationStore(async () => ({ events: flood.slice().reverse() }), src)
  await store.refresh()
  const snap = store.getSnapshot()
  expect(snap.error).toBeNull()
  expect(snap.workStates.worker1).toMatchObject({ working: true })
  expect(snap.events.some(e => e.sessionId === 'worker1')).toBe(true)
  expect(src.queries.every(q => new URLSearchParams(q).get('session_id'))).toBe(true)
  expect(src.queries.filter(q => new URLSearchParams(q).get('limit') === '25')).toHaveLength(2)
})
it('a claude-stream session that only reports work state shows as working, then idle, and is dropped when it stops', async () => {
  vi.useFakeTimers(); vi.setSystemTime(now)
  const rows = { stream1: [sessionRow(5, 'stream1', 'session.work_state_changed', { from: 'idle', to: 'processing' })] }
  let running = ['stream1']
  const src = { ...source(rows), running: async () => running }
  const store = createObservationStore(async () => ({ events: [] }), src)
  await store.refresh()
  const agents = parseFleetAgents({ agents: [{ id: 'tether:stream1', session_id: 'stream1', state: 'unknown', cwd: '' }] })
  const shown = () => withObservedActivity(agents[0], 'stream1', store.getSnapshot())
  expect(shown().state).toBe('busy')
  rows.stream1.push(sessionRow(6, 'stream1', 'session.work_state_changed', { from: 'processing', to: 'idle' }))
  await store.refresh()
  expect(shown().state).toBe('idle')
  running = []
  await store.refresh()
  expect(store.getSnapshot().workStates.stream1).toBeUndefined()
  expect(shown().state).toBe('unknown')
})
it('keeps the last work state when one session read fails, and never overrides a monitored state', async () => {
  vi.useFakeTimers(); vi.setSystemTime(now)
  const src = source({ w: [sessionRow(1, 'w', 'session.work_state_changed', { to: 'processing' })] })
  const store = createObservationStore(async () => ({ events: [] }), src)
  await store.refresh()
  src.page = async () => { throw new Error('boom') }
  await store.refresh()
  expect(store.getSnapshot().workStates.w?.working).toBe(true)
  expect(store.getSnapshot().error).toBeNull()
  const [monitored] = parseFleetAgents({ agents: [{ id: 'm', session_id: 'w', state: 'waiting', cwd: '' }] })
  expect(withObservedActivity(monitored, 'w', store.getSnapshot()).state).toBe(monitored.state)
})

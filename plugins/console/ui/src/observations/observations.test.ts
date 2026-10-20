import { afterEach, expect, it, vi } from 'vitest'
import { createObservationStore, eventFromRow } from './store'
import { activityText, presence, toolLabel } from './model'
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

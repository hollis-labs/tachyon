import { describe, expect, it, vi } from 'vitest'
import { sessionThreadId, withChatApiMessages } from './chatapi'
import type { ChatClient, ChatEvent } from './types'

const base = (): ChatClient => ({
  listThreads: async () => [], openThread: vi.fn(),
  history: vi.fn(async () => ({ messages: [], hasMore: false })), send: vi.fn(),
  markRead: vi.fn(async () => undefined), subscribe: vi.fn(() => () => undefined), subscribeThreads: vi.fn(() => () => undefined),
})
const id = sessionThreadId('tether:s1')
const m = (seq: number, role: 'agent' | 'operator' = 'agent') => ({ seq, id: `m_${seq}`, role, kind: 'text', text: `t${seq}`, at: 1000 + seq })

class FakeES {
  onopen: (() => void) | null = null
  onerror: (() => void) | null = null
  onmessage: ((e: { data: string }) => void) | null = null
  close = vi.fn()
}

describe('withChatApiMessages', () => {
  it('passes other conversations to the wrapped client', async () => {
    const b = base()
    await withChatApiMessages(b).history('chat://tether/launch/x', {})
    expect(b.history).toHaveBeenCalled()
  })
  it('pages older messages by the oldest seq already loaded', async () => {
    const fetchPage = vi.fn(async (_r: string, q: { before_seq?: number }) => ({ messages: q.before_seq ? [m(1)] : [m(5), m(6)], has_more: !q.before_seq }))
    const c = withChatApiMessages(base(), { fetchPage })
    const first = await c.history(id, { limit: 2 })
    expect(first).toMatchObject({ hasMore: true })
    await c.history(id, { before: first.messages[0].at, limit: 2 })
    expect(fetchPage).toHaveBeenLastCalledWith('tether:s1', { limit: 2, before_seq: 5 })
  })
  it('reuses the idempotency key for a retry of the same text and a new one after success', async () => {
    const post = vi.fn().mockRejectedValueOnce(new Error('down')).mockResolvedValue({ id: 'm_9' })
    let n = 0
    const c = withChatApiMessages(base(), { post, newKey: () => `k${++n}` })
    await expect(c.send(id, 'hi')).rejects.toThrow('down')
    const sent = await c.send(id, 'hi')
    await c.send(id, 'hi')
    expect(post.mock.calls.map((x) => x[1].idempotency_key)).toEqual(['k1', 'k1', 'k2'])
    expect(sent).toMatchObject({ id: 'm_9', from: 'operator', delivery: 'queued' })
  })
  it('refetches on frames and after a reconnect without duplicating messages', async () => {
    vi.stubGlobal('EventSource', FakeES)
    const es = new FakeES()
    let rows = [m(1)]
    const fetchPage = vi.fn(async () => ({ messages: rows }))
    const events: ChatEvent[] = []
    const off = withChatApiMessages(base(), { fetchPage, stream: () => es as unknown as EventSource }).subscribe(id, (e) => events.push(e))
    await vi.waitFor(() => expect(fetchPage).toHaveBeenCalledTimes(1))
    rows = [m(1), m(2)]
    es.onmessage?.({ data: JSON.stringify({ type: 'message', thread_ref: 'tether:s1' }) })
    await vi.waitFor(() => expect(fetchPage).toHaveBeenCalledTimes(2))
    es.onerror?.()
    es.onopen?.()
    await vi.waitFor(() => expect(fetchPage).toHaveBeenCalledTimes(3))
    const ids = events.filter((e) => e.type === 'message').map((e) => (e as { message: { id: string } }).message.id)
    expect(new Set(ids)).toEqual(new Set(['m_1', 'm_2']))
    off()
    expect(es.close).toHaveBeenCalled()
    vi.unstubAllGlobals()
  })
  it(polls while open so Tether-mirrored replies show without a stream frame, async () => {
    vi.useFakeTimers()
    vi.stubGlobal(EventSource, FakeES)
    const fetchPage = vi.fn(async () => ({ messages: [m(1)] }))
    const off = withChatApiMessages(base(), { fetchPage, stream: () => new FakeES() as unknown as EventSource }).subscribe(id, () => undefined)
    await vi.advanceTimersByTimeAsync(9000)
    expect(fetchPage.mock.calls.length).toBeGreaterThanOrEqual(3)
    off()
    const n = fetchPage.mock.calls.length
    await vi.advanceTimersByTimeAsync(9000)
    expect(fetchPage).toHaveBeenCalledTimes(n)
    vi.useRealTimers()
    vi.unstubAllGlobals()
  })
})

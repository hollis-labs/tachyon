/** @vitest-environment jsdom */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createAppAgentChatClient } from './appagent'
import { createCompositeChatClient } from './composite'
import type { ChatClient, ChatEvent, ChatMessage, ChatThread } from './types'

const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
const wire = (id: string, seq: number, over: Partial<ChatMessage> = {}) => ({ id, seq, threadId: 'app-tachyon', from: 'agent', body: id, at: seq, tools: [], ...over })
const thread = (id: string, lastAt: number): ChatThread => ({
  id, title: id, unread: 0, lastAt, agent: { address: id.startsWith('app-') ? 'tachyon://agent/tachyon' : `chat://tether/launch/${id}`, label: id, kind: 'agent' },
  capability: { mode: 'live', canSend: true },
})

/** A stream whose chunks the test controls. */
function sse() {
  let ctrl!: ReadableStreamDefaultController<Uint8Array>
  const enc = new TextEncoder()
  const body = new ReadableStream<Uint8Array>({ start: (c) => { ctrl = c } })
  return { body, push: (s: string) => ctrl.enqueue(enc.encode(s)), close: () => ctrl.close() }
}
const frame = (e: ChatEvent) => `data: ${JSON.stringify(e)}\n\n`
const tick = (ms = 0) => vi.advanceTimersByTimeAsync(ms)

beforeEach(() => vi.useFakeTimers())
afterEach(() => vi.useRealTimers())

describe('app agent ChatClient', () => {
  it('lists threads, and treats a server without the feature as no threads', async () => {
    const f = vi.fn()
      .mockResolvedValueOnce(json({ threads: [thread('app-tachyon', 5)] }))
      .mockResolvedValueOnce(json({ error: { code: 'not_configured', message: 'off' } }, 503))
      .mockResolvedValueOnce(json({ error: { code: 'internal', message: 'boom' } }, 500))
    const c = createAppAgentChatClient(f as never)
    expect((await c.listThreads()).map((t) => t.id)).toEqual(['app-tachyon'])
    expect(await c.listThreads()).toEqual([])
    await expect(c.listThreads()).rejects.toThrow('boom')
  })

  it('opens the thread for an app agent address and refuses an unknown one', async () => {
    const f = vi.fn().mockImplementation(async () => json({ threads: [thread('app-tachyon', 5)] }))
    const c = createAppAgentChatClient(f as never)
    expect((await c.openThread({ address: 'tachyon://agent/tachyon', label: 'x', kind: 'agent' })).id).toBe('app-tachyon')
    await expect(c.openThread({ address: 'tachyon://agent/other', label: 'x', kind: 'agent' })).rejects.toThrow('not available')
  })

  it('pages history by sequence, newest first then older, without leaking seq', async () => {
    const f = vi.fn()
      .mockResolvedValueOnce(json({ messages: [wire('m3', 3), wire('m4', 4)], has_more: true }))
      .mockResolvedValueOnce(json({ messages: [wire('m1', 1), wire('m2', 2)], has_more: false }))
    const c = createAppAgentChatClient(f as never)
    const first = await c.history('app-tachyon', { limit: 2 })
    expect(first.hasMore).toBe(true)
    expect(first.messages.map((m) => m.id)).toEqual(['m3', 'm4'])
    expect('seq' in first.messages[0]).toBe(false)
    const older = await c.history('app-tachyon', { before: first.messages[0].at, limit: 2 })
    expect(older.messages.map((m) => m.id)).toEqual(['m1', 'm2'])
    expect(String(f.mock.calls[1][0])).toContain('before_seq=3')
    expect(String(f.mock.calls[0][0])).not.toContain('before_seq')
  })

  it('sends, and adds the reply only when the stream has not already shown it', async () => {
    const f = vi.fn().mockImplementation(async (url: string, init?: RequestInit) => {
      if (String(url).endsWith('/events')) return new Response(sse().body, { status: 200 })
      expect(init?.method).toBe('POST')
      expect(JSON.parse(String(init?.body))).toEqual({ body: 'hi' })
      return json({ message: wire('op1', 1, { from: 'operator', delivery: 'queued' }), reply: wire('ag1', 2, { body: '', streaming: true }) }, 202)
    })
    const c = createAppAgentChatClient(f as never)
    const got: ChatEvent[] = []
    const off = c.subscribe('app-tachyon', (e) => got.push(e))
    await tick()
    const m = await c.send('app-tachyon', 'hi')
    expect(m.id).toBe('op1')
    expect(got.filter((e) => e.type === 'message').map((e) => (e as { message: ChatMessage }).message.id)).toEqual(['ag1'])
    // Now the stream has shown ag2 first: send must not emit its empty snapshot over streamed text.
    const s = sse()
    f.mockImplementation(async (url: string) => (String(url).endsWith('/events') ? new Response(s.body, { status: 200 }) : json({ message: wire('op2', 3, { from: 'operator' }), reply: wire('ag2', 4, { body: '', streaming: true }) }, 202)))
    off()
    const c2 = createAppAgentChatClient(f as never)
    const got2: ChatEvent[] = []
    c2.subscribe('app-tachyon', (e) => got2.push(e))
    await tick()
    s.push(frame({ type: 'message', message: { ...wire('ag2', 4, { body: 'Hel', streaming: true }) } as never }))
    await tick()
    await c2.send('app-tachyon', 'again')
    const snapshots = got2.filter((e) => e.type === 'message') as { message: ChatMessage }[]
    expect(snapshots).toHaveLength(1)
    expect(snapshots[0].message.body).toBe('Hel')
  })

  it('reads SSE frames split across chunks and stops when the last listener leaves', async () => {
    const s = sse()
    let signal: AbortSignal | undefined
    const f = vi.fn().mockImplementation(async (_u: string, init?: RequestInit) => { signal = init?.signal ?? undefined; return new Response(s.body, { status: 200 }) })
    const c = createAppAgentChatClient(f as never)
    const got: ChatEvent[] = []
    const off = c.subscribe('app-tachyon', (e) => got.push(e))
    await tick()
    const d = frame({ type: 'delta', messageId: 'ag1', text: 'Hello' })
    s.push(d.slice(0, 15))
    await tick()
    expect(got.filter((e) => e.type === 'delta')).toHaveLength(0)
    s.push(d.slice(15) + ': keepalive\n\n' + frame({ type: 'tool', messageId: 'ag1', tool: { id: 'c', name: 't', status: 'ok' } }) + frame({ type: 'end', messageId: 'ag1' }))
    await tick()
    expect(got.map((e) => e.type)).toEqual(['error', 'delta', 'tool', 'end'])
    expect((got[0] as { message: string | null }).message).toBeNull()
    off()
    expect(signal?.aborted).toBe(true)
  })

  it('reports a dropped stream, reconnects with backoff and resyncs what finished meanwhile', async () => {
    const first = sse()
    const second = sse()
    const streams = [first, second]
    const f = vi.fn().mockImplementation(async (url: string) => {
      if (String(url).includes('/messages')) return json({ messages: [wire('ag1', 2, { body: 'final answer' })], has_more: false })
      return new Response(streams.shift()!.body, { status: 200 })
    })
    const c = createAppAgentChatClient(f as never)
    const got: ChatEvent[] = []
    c.subscribe('app-tachyon', (e) => got.push(e))
    await tick()
    first.close()
    await tick()
    expect(got.at(-1)).toEqual({ type: 'error', message: 'Live updates paused. Reconnecting.' })
    await tick(1100)
    expect(f.mock.calls.filter(([u]) => String(u).includes('/events'))).toHaveLength(2)
    const resynced = got.find((e) => e.type === 'message') as { message: ChatMessage } | undefined
    expect(resynced?.message.body).toBe('final answer')
    expect(got.at(-1)).toEqual(expect.objectContaining({ type: 'message' }))
    expect(got.filter((e) => e.type === 'error' && (e as { message: string | null }).message === null)).toHaveLength(2)
  })

  it('polls threads and reports failures', async () => {
    const f = vi.fn().mockResolvedValueOnce(json({ threads: [thread('app-tachyon', 1)] })).mockResolvedValueOnce(json({ error: { message: 'down' } }, 500))
    const c = createAppAgentChatClient(f as never)
    const lists: string[][] = []
    const errors: string[] = []
    const off = c.subscribeThreads((t) => lists.push(t.map((x) => x.id)), (m) => errors.push(m))
    await tick(5000)
    await tick(5000)
    off()
    await tick(10000)
    expect(lists).toEqual([['app-tachyon']])
    expect(errors).toEqual(['down'])
    expect(f).toHaveBeenCalledTimes(2)
  })
})

describe('composite ChatClient', () => {
  const stub = (id: string, lastAt: number, fail = false): ChatClient & { calls: string[] } => {
    const calls: string[] = []
    return {
      calls,
      listThreads: async () => { if (fail) throw new Error('down'); return [thread(id, lastAt)] },
      openThread: async () => { calls.push('open'); return thread(id, lastAt) },
      history: async () => { calls.push('history'); return { messages: [], hasMore: false } },
      send: async () => { calls.push('send'); return wire('m', 1) as never },
      markRead: async () => { calls.push('read') },
      subscribe: () => { calls.push('subscribe'); return () => calls.push('unsubscribe') },
      subscribeThreads: (l) => { l([thread(id, lastAt)]); return () => {} },
    }
  }

  it('merges threads newest first and routes calls by thread id and agent address', async () => {
    const tether = stub('chat-1', 10)
    const app = stub('app-tachyon', 20)
    const c = createCompositeChatClient(tether, app)
    expect((await c.listThreads()).map((t) => t.id)).toEqual(['app-tachyon', 'chat-1'])
    await c.history('app-tachyon'); await c.send('app-tachyon', 'x'); await c.markRead('app-tachyon'); c.subscribe('app-tachyon', () => {})
    await c.history('chat-1'); await c.send('chat-1', 'x')
    await c.openThread({ address: 'tachyon://agent/tachyon', label: 'x', kind: 'agent' })
    await c.openThread({ address: 'chat://tether/launch/1', label: 'x', kind: 'agent' })
    expect(app.calls).toEqual(['history', 'send', 'read', 'subscribe', 'open'])
    expect(tether.calls).toEqual(['history', 'send', 'open'])
  })

  it('keeps the primary threads when the app agent is down, and merges live updates', async () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => undefined)
    const c = createCompositeChatClient(stub('chat-1', 10), stub('app-tachyon', 20, true))
    expect((await c.listThreads()).map((t) => t.id)).toEqual(['chat-1'])
    expect(warn).toHaveBeenCalledWith('App agent threads unavailable:', 'down')
    warn.mockRestore()
    const seen: string[][] = []
    createCompositeChatClient(stub('chat-1', 10), stub('app-tachyon', 20)).subscribeThreads((t) => seen.push(t.map((x) => x.id)))
    expect(seen.at(-1)).toEqual(['app-tachyon', 'chat-1'])
  })
})

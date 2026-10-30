import { CONSOLE_API_BASE, consoleJson } from '../lib/http'
import type { ChatClient, ChatEvent, ChatMessage, HistoryQuery } from './types'

/** A conversation id for one chat API session thread. Everything else goes to the wrapped client. */
const PREFIX = 'chatapi:'
export const sessionThreadId = (ref: string) => PREFIX + ref
export const isSessionThread = (id: string) => id.startsWith(PREFIX)
const refOf = (id: string) => id.slice(PREFIX.length)

interface ApiMessage {
  seq: number
  id: string
  role: 'operator' | 'agent' | 'system'
  kind?: string
  text?: string
  at: number
}
interface Page {
  messages?: ApiMessage[]
  has_more?: boolean
}

const path = (ref: string, tail: string) => `/chat/threads/${encodeURIComponent(ref)}${tail}`

export function toMessage(threadId: string, m: ApiMessage): ChatMessage {
  return {
    id: m.id,
    threadId,
    from: m.role,
    body: m.text ?? '',
    at: m.at,
    tools: [],
    ...(m.role === 'operator' ? { delivery: 'delivered' as const } : {}),
    info: { transport: 'tether-chat', kind: m.kind, sequence: m.seq },
  }
}

/** Frames that mean "the messages changed". Live frames are not replayed, so each one is a prompt to refetch. */
const CHANGES = new Set(['message', 'delta', 'tool', 'end', 'delivery', 'card', 'thread'])

export interface ChatApiOptions {
  fetchPage?: (ref: string, q: { before_seq?: number; limit: number }) => Promise<Page>
  post?: (ref: string, body: { text: string; idempotency_key: string }) => Promise<{ id: string }>
  markRead?: (ref: string) => Promise<unknown>
  stream?: (ref: string) => EventSource
  newKey?: () => string
}

/**
 * Messages, sending, read state and the live stream for one chat API session thread (`chatapi:<thread_ref>`).
 * Other conversations pass through to `base`.
 * - Frames are signals: each one refetches the newest page, and messages merge by server id, so a replayed or
 *   doubled frame cannot duplicate a message.
 * - A reconnect refetches too (nothing live is replayed).
 * - A send keeps its idempotency key until it is accepted, so a retry of the same text is the same message.
 */
export function withChatApiMessages(base: ChatClient, o: ChatApiOptions = {}): ChatClient {
  const fetchPage =
    o.fetchPage ??
    ((ref, q) => consoleJson<Page>(path(ref, `/messages?limit=${q.limit}${q.before_seq ? `&before_seq=${q.before_seq}` : ''}`)))
  const post =
    o.post ?? ((ref, body) => consoleJson<{ id: string }>(path(ref, '/messages'), { method: 'POST', body }))
  const markRead = o.markRead ?? ((ref) => consoleJson(path(ref, '/read'), { method: 'POST' }))
  const stream = o.stream ?? ((ref) => new EventSource(`${CONSOLE_API_BASE}${path(ref, '/events')}`))
  const newKey = o.newKey ?? (() => crypto.randomUUID())
  const oldestSeq = new Map<string, number>()
  const pending = new Map<string, { body: string; key: string }>()

  return {
    ...base,
    async history(id, q: HistoryQuery = {}) {
      if (!isSessionThread(id)) return base.history(id, q)
      const ref = refOf(id)
      const older = q.before !== undefined
      const page = await fetchPage(ref, { limit: q.limit ?? 20, before_seq: older ? oldestSeq.get(id) : undefined })
      const rows = page.messages ?? []
      const min = rows.reduce((n, m) => Math.min(n, m.seq), Infinity)
      if (Number.isFinite(min) && (older || !oldestSeq.has(id))) oldestSeq.set(id, min)
      return { messages: rows.map((m) => toMessage(id, m)).sort((a, b) => a.at - b.at || a.id.localeCompare(b.id)), hasMore: !!page.has_more }
    },
    async send(id, body, opts) {
      if (!isSessionThread(id)) return base.send(id, body, opts)
      let p = pending.get(id)
      if (p?.body !== body) {
        p = { body, key: newKey() }
        pending.set(id, p)
      }
      const accepted = await post(refOf(id), { text: body, idempotency_key: p.key })
      pending.delete(id)
      return { id: accepted.id, threadId: id, from: 'operator', body, at: Date.now(), delivery: 'queued', tools: [], info: { transport: 'tether-chat', idempotencyKey: p.key } }
    },
    async markRead(id) {
      if (!isSessionThread(id)) return base.markRead(id)
      await markRead(refOf(id)).catch(() => undefined)
    },
    subscribe(id, listener: (e: ChatEvent) => void) {
      if (!isSessionThread(id)) return base.subscribe(id, listener)
      const ref = refOf(id)
      let live = true
      let busy = false
      let again = false
      const sync = async () => {
        if (busy) return void (again = true)
        busy = true
        try {
          const page = await fetchPage(ref, { limit: 20 })
          if (!live) return
          listener({ type: 'error', message: null })
          for (const m of page.messages ?? []) listener({ type: 'message', message: toMessage(id, m) })
        } catch (e) {
          if (live) listener({ type: 'error', message: e instanceof Error ? e.message : 'Chat unavailable' })
        } finally {
          busy = false
          if (again && live) {
            again = false
            void sync()
          }
        }
      }
      void sync()
      let es: EventSource | null = null
      if (typeof EventSource !== 'undefined') {
        es = stream(ref)
        let dropped = false
        es.onopen = () => {
          if (dropped) void sync()
          dropped = false
        }
        es.onerror = () => {
          dropped = true
        }
        es.onmessage = (m) => {
          try {
            if (CHANGES.has((JSON.parse(m.data) as { type?: string }).type ?? '')) void sync()
          } catch {
            /* a malformed frame is skipped */
          }
        }
      }
      return () => {
        live = false
        es?.close()
      }
    },
  }
}

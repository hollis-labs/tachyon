// ChatClient for Tachyon's own in-app agent. Tachyon owns the threads and history;
// the runtime behind the agent (Nanite today) is invisible here. Server contract:
// internal/appagent (/api/app-agent/v1). Transport details stay in this file.

import { operatorFetch } from '../operator/elevation'
import type { Recipient } from '../messaging'
import type { ChatClient, ChatEvent, ChatMessage, ChatThread, HistoryPage, HistoryQuery } from './types'

export const APP_AGENT_BASE = '/api/app-agent/v1'
export const APP_THREAD_PREFIX = 'app-'
export const APP_AGENT_ADDRESS_PREFIX = 'tachyon://agent/'
export const isAppThread = (threadId: string) => threadId.startsWith(APP_THREAD_PREFIX)
export const isAppAgent = (agent: Recipient) => agent.address.startsWith(APP_AGENT_ADDRESS_PREFIX)

/** The server has no app agent config: show nothing, not an error. */
class NotConfigured extends Error {}

interface WireMessage extends ChatMessage {
  seq: number
}

const THREAD_POLL_MS = 5000
const RETRY_MIN_MS = 1000
const RETRY_MAX_MS = 10000

async function api<T>(f: typeof fetch, path: string, init?: RequestInit): Promise<T> {
  const res = await f(APP_AGENT_BASE + path, {
    ...init,
    headers: { Accept: 'application/json', ...(init?.body ? { 'Content-Type': 'application/json' } : {}), ...init?.headers },
  })
  const body = await res.json().catch(() => null)
  if (!res.ok) {
    const e = body?.error
    if (res.status === 503 && e?.code === 'not_configured') throw new NotConfigured(e.message)
    throw new Error(e?.message ?? `The app agent request failed (${res.status})`)
  }
  return body as T
}

function sleep(ms: number, signal: AbortSignal): Promise<void> {
  return new Promise((resolve) => {
    const t = setTimeout(resolve, ms)
    signal.addEventListener('abort', () => { clearTimeout(t); resolve() }, { once: true })
  })
}

/** Reads `data:` frames from a text/event-stream body; frames may be split across chunks. */
async function readFrames(body: ReadableStream<Uint8Array>, signal: AbortSignal, onData: (json: string) => void): Promise<void> {
  const reader = body.getReader()
  const decoder = new TextDecoder()
  let buf = ''
  try {
    for (;;) {
      const { done, value } = await reader.read()
      if (done || signal.aborted) return
      buf += decoder.decode(value, { stream: true })
      let i: number
      while ((i = buf.indexOf('\n\n')) >= 0) {
        const frame = buf.slice(0, i)
        buf = buf.slice(i + 2)
        for (const line of frame.split('\n')) {
          if (line.startsWith('data:')) onData(line.slice(5).trim())
        }
      }
    }
  } finally {
    reader.releaseLock()
  }
}

export function createAppAgentChatClient(f: typeof fetch = operatorFetch): ChatClient {
  const listeners = new Map<string, Set<(e: ChatEvent) => void>>()
  const streams = new Map<string, AbortController>()
  const seen = new Map<string, Set<string>>() // thread -> message ids the stream has shown
  const oldestSeq = new Map<string, number>()

  const emit = (threadId: string, e: ChatEvent) => listeners.get(threadId)?.forEach((fn) => fn(e))
  const seenIn = (threadId: string) => {
    let s = seen.get(threadId)
    if (!s) seen.set(threadId, (s = new Set()))
    return s
  }
  const strip = ({ seq: _seq, ...m }: WireMessage): ChatMessage => m

  const listThreads = async (): Promise<ChatThread[]> => {
    try {
      const r = await api<{ threads: ChatThread[] }>(f, '/threads')
      return r.threads
    } catch (e) {
      if (e instanceof NotConfigured) return []
      throw e
    }
  }

  const history = async (threadId: string, q?: HistoryQuery): Promise<HistoryPage> => {
    const qs = new URLSearchParams({ limit: String(q?.limit ?? 20) })
    const before = q?.before ? oldestSeq.get(threadId) : undefined
    if (before) qs.set('before_seq', String(before))
    const r = await api<{ messages: WireMessage[]; has_more: boolean }>(f, `/threads/${encodeURIComponent(threadId)}/messages?${qs}`)
    if (r.messages.length) {
      const min = Math.min(...r.messages.map((m) => m.seq))
      oldestSeq.set(threadId, before ? Math.min(before, min) : min)
    }
    return { messages: r.messages.map(strip), hasMore: r.has_more }
  }

  /** After a dropped stream, pick up whatever finished while we were away. */
  const resync = async (threadId: string) => {
    const r = await api<{ messages: WireMessage[] }>(f, `/threads/${encodeURIComponent(threadId)}/messages?limit=20`)
    for (const m of r.messages) emit(threadId, { type: 'message', message: strip(m) })
  }

  async function run(threadId: string, ac: AbortController) {
    let delay = RETRY_MIN_MS
    let first = true
    while (!ac.signal.aborted) {
      try {
        const res = await f(`${APP_AGENT_BASE}/threads/${encodeURIComponent(threadId)}/events`, { headers: { Accept: 'text/event-stream' }, signal: ac.signal })
        if (!res.ok || !res.body) throw new Error(`HTTP ${res.status}`)
        emit(threadId, { type: 'error', message: null })
        if (!first) await resync(threadId)
        first = false
        delay = RETRY_MIN_MS
        await readFrames(res.body, ac.signal, (json) => {
          let e: ChatEvent
          try { e = JSON.parse(json) as ChatEvent } catch { return }
          if (e.type === 'message') seenIn(threadId).add(e.message.id)
          if (e.type === 'delta') seenIn(threadId).add(e.messageId)
          emit(threadId, e)
        })
      } catch {
        if (ac.signal.aborted) return
      }
      if (ac.signal.aborted) return
      emit(threadId, { type: 'error', message: 'Live updates paused. Reconnecting.' })
      await sleep(delay, ac.signal)
      delay = Math.min(delay * 2, RETRY_MAX_MS)
    }
  }

  return {
    listThreads,
    async openThread(agent: Recipient) {
      const t = (await listThreads()).find((t) => t.agent.address === agent.address)
      if (!t) throw new Error('That app agent is not available')
      return t
    },
    history,
    async send(threadId, body) {
      // opts.context is reserved for what the operator is viewing; nothing reads it yet.
      const r = await api<{ message: WireMessage; reply: WireMessage }>(f, `/threads/${encodeURIComponent(threadId)}/messages`, { method: 'POST', body: JSON.stringify({ body }) })
      // The stream normally delivers the reply first. If it has not connected yet, add it now,
      // but never over a reply the stream already started (that would wipe its text).
      if (!seenIn(threadId).has(r.reply.id)) emit(threadId, { type: 'message', message: strip(r.reply) })
      return strip(r.message)
    },
    async markRead(threadId) {
      await api(f, `/threads/${encodeURIComponent(threadId)}/read`, { method: 'POST' }).catch(() => undefined)
    },
    subscribe(threadId, listener) {
      let set = listeners.get(threadId)
      if (!set) listeners.set(threadId, (set = new Set()))
      set.add(listener)
      if (!streams.has(threadId)) {
        const ac = new AbortController()
        streams.set(threadId, ac)
        void run(threadId, ac)
      }
      return () => {
        set.delete(listener)
        if (set.size === 0) {
          listeners.delete(threadId)
          streams.get(threadId)?.abort()
          streams.delete(threadId)
          seen.delete(threadId)
        }
      }
    },
    subscribeThreads(listener, onError) {
      let live = true
      const tick = () => listThreads().then((t) => { if (live) listener(t) }, (e) => { if (live) onError?.(e instanceof Error ? e.message : String(e)) })
      const timer = setInterval(tick, THREAD_POLL_MS)
      return () => { live = false; clearInterval(timer) }
    },
  }
}

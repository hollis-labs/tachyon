import { useCallback, useEffect, useRef, useState } from 'react'
import type { Collector, Session, SessionsResponse, StreamStatus } from './types'

export const AGENTS_BASE = '/api/plugins/console/v1/agents'
const STREAM_TYPES = ['session', 'summary', 'collector', 'bus', 'control', 'gap'] as const

export interface StreamEvent {
  type: (typeof STREAM_TYPES)[number]
  at: number
  session?: Partial<Session> & { id: string }
  summary?: SessionsResponse['summary']
  collector?: Partial<Collector>
}

/** The server sends null for an empty list and "" for unknown text; the UI reads arrays and never a null list. */
export function normalizeSessions(r: SessionsResponse): SessionsResponse {
  return { ...r, summary: r.summary ?? {}, sessions: (r.sessions ?? []).map((s) => ({ ...s, spark: s.spark ?? [], needs_you: s.needs_you ?? { reason: null, since: null }, context: s.context ?? { used: null, window: null, pct: null, source: 'unknown', breakdown: null }, tokens: s.tokens ?? { metered: false, input: null, output: null, cache_read: null, cache_write: null }, pending: s.pending ?? { approval: false, question: false, known: false }, capabilities: s.capabilities ?? { nudge: false, interrupt: { available: false, reason: null }, restart: false } })) }
}

const isFull = (p: Partial<Session> & { id: string }): p is Session => !!p.agent && !!p.needs_you && !!p.context

/** Applies one live snapshot to the held data. bus, control and gap do not change the sessions list. */
export function applyStreamEvent(data: SessionsResponse, ev: StreamEvent): SessionsResponse {
  if (ev.type === 'session' && ev.session) {
    const part = ev.session
    const known = data.sessions.some((s) => s.id === part.id)
    if (!known) return isFull(part) ? { ...data, sessions: [...data.sessions, part] } : data // a partial snapshot of an unknown session is not shown; the hook reads the list again
    return { ...data, sessions: data.sessions.map((s) => (s.id === part.id ? { ...s, ...part } : s)) }
  }
  if (ev.type === 'summary' && ev.summary) return { ...data, summary: ev.summary }
  if (ev.type === 'collector' && ev.collector) return { ...data, collector: { ...data.collector, ...ev.collector } }
  return data
}

/**
 * The sessions snapshot kept fresh by the global stream. After a gap, or when
 * the stream comes back, the snapshot is read again: replay is not trusted to
 * cover what was missed.
 */
export function useAgentsMonitor() {
  const [data, setData] = useState<SessionsResponse | null>(null)
  const [status, setStatus] = useState<StreamStatus>('loading')
  const [error, setError] = useState<string | null>(null)
  const held = useRef<SessionsResponse | null>(null)
  const [busTick, setBusTick] = useState(0) // bumps on every bus event so the Activity tab reads its head again

  const refetch = useCallback(async () => {
    try {
      const r = await fetch(`${AGENTS_BASE}/sessions`)
      if (!r.ok) throw new Error(`The server answered ${r.status}`)
      setData(normalizeSessions((await r.json()) as SessionsResponse))
      setError(null)
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Could not load')
    }
  }, [])

  useEffect(() => {
    void refetch()
    if (typeof EventSource === 'undefined') return
    const es = new EventSource(`${AGENTS_BASE}/stream`)
    let dropped = false
    const onEvent = (e: MessageEvent) => {
      let ev: StreamEvent
      try {
        ev = JSON.parse(e.data) as StreamEvent
      } catch {
        return
      }
      if (ev.type === 'gap') void refetch()
      else if (ev.type === 'bus') setBusTick((n) => n + 1)
      else {
        if (ev.type === 'session' && ev.session && held.current && !held.current.sessions.some((x) => x.id === ev.session?.id) && !isFull(ev.session)) void refetch()
        setData((d) => (d ? applyStreamEvent(d, ev) : d))
      }
    }
    es.onopen = () => {
      setStatus('live')
      if (dropped) void refetch()
      dropped = false
    }
    es.onerror = () => {
      dropped = true
      setStatus('reconnecting')
    }
    es.onmessage = onEvent
    for (const t of STREAM_TYPES) es.addEventListener(t, onEvent as EventListener)
    return () => es.close()
  }, [refetch])

  held.current = data
  // First data decides between loading and error; a later failure keeps the last data.
  const shown: StreamStatus = data === null ? (error ? 'error' : 'loading') : status === 'reconnecting' || error ? 'reconnecting' : 'live'
  return { data, status: shown, error, refetch, busTick }
}

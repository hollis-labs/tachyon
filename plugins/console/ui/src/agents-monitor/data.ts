import { useCallback, useEffect, useState } from 'react'
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

/** Applies one live snapshot to the held data. bus, control and gap do not change the sessions list. */
export function applyStreamEvent(data: SessionsResponse, ev: StreamEvent): SessionsResponse {
  if (ev.type === 'session' && ev.session) {
    const part = ev.session
    const known = data.sessions.some((s) => s.id === part.id)
    return { ...data, sessions: known ? data.sessions.map((s) => (s.id === part.id ? { ...s, ...part } : s)) : [...data.sessions, part as Session] }
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

  const refetch = useCallback(async () => {
    try {
      const r = await fetch(`${AGENTS_BASE}/sessions`)
      if (!r.ok) throw new Error(`The server answered ${r.status}`)
      setData((await r.json()) as SessionsResponse)
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
      else setData((d) => (d ? applyStreamEvent(d, ev) : d))
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

  // First data decides between loading and error; a later failure keeps the last data.
  const shown: StreamStatus = data === null ? (error ? 'error' : 'loading') : status === 'reconnecting' || error ? 'reconnecting' : 'live'
  return { data, status: shown, error, refetch }
}

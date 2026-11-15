import { useEffect, useRef, useState } from 'react'
import { BUILDER_BASE } from './api'

// GET /builder/stream: flat JSON {type, at, ...} on the default SSE message. change and catalog carry
// ids the browser replays after a reconnect; summary is a live snapshot (refetch after a gap).
export type StreamEvent =
  | { type: 'change'; at: number; change_id: string; state: string }
  | { type: 'catalog'; at: number; observed_at?: number; changed_paths?: string[]; source?: 'builder' | 'observed' }
  | { type: 'summary'; at: number; [k: string]: unknown }

export function parseEvent(data: string): StreamEvent | null {
  try {
    const e = JSON.parse(data) as { type?: unknown; at?: unknown }
    if (typeof e.type !== 'string' || typeof e.at !== 'number') return null
    if (e.type === 'change' || e.type === 'catalog' || e.type === 'summary') return e as StreamEvent
  } catch {
    // not an event we know
  }
  return null
}

/** Subscribe to builder events. Returns whether the stream is currently connected. */
export function useBuilderStream(onEvent: (e: StreamEvent) => void): boolean {
  const latest = useRef(onEvent)
  latest.current = onEvent
  const [live, setLive] = useState(false)
  useEffect(() => {
    if (typeof EventSource === 'undefined') return
    const es = new EventSource(`${BUILDER_BASE}/stream`)
    es.onopen = () => setLive(true)
    es.onerror = () => setLive(false) // the browser reconnects and replays from Last-Event-ID
    es.onmessage = (m) => {
      const e = parseEvent(String(m.data))
      if (e) latest.current(e)
    }
    return () => es.close()
  }, [])
  return live
}

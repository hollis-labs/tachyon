import { useEffect, useRef } from 'react'
import { CHAT_BASE } from './roster'

/** One server frame: the Event envelope of internal/chatapi/openapi.yaml (`type`, `thread_ref`, `at`, then the payload beside them). */
export interface Frame { type: string; thread_ref?: string; [k: string]: unknown }
type Listener = (f: Frame) => void
export interface Hub { subscribe: (l: Listener) => () => void }

/**
 * One EventSource per stream URL, shared by every subscriber and closed with the last one. Frames are signals, not
 * state: a reconnect after a drop delivers a synthetic `{type:'reconnected'}` so subscribers refetch what they show
 * (live-only frames are not replayed; activity-class frames replay through Last-Event-ID, which the browser sends).
 */
function hub(url: string, make: (u: string) => EventSource = (u) => new EventSource(u)): Hub {
  const ls = new Set<Listener>()
  let es: EventSource | null = null
  let dropped = false
  const open = () => {
    es = make(url)
    es.onopen = () => { if (dropped) ls.forEach((l) => l({ type: 'reconnected' })); dropped = false }
    es.onerror = () => { dropped = true }
    es.onmessage = (m) => {
      let f: Frame
      try { f = JSON.parse(m.data) as Frame } catch { return }
      if (f && typeof f.type === 'string') ls.forEach((l) => l(f))
    }
  }
  return {
    subscribe(l) {
      ls.add(l)
      if (!es && typeof EventSource !== 'undefined') open()
      return () => { ls.delete(l); if (!ls.size) { es?.close(); es = null } }
    },
  }
}

const hubs = new Map<string, Hub>()
const shared = (url: string) => { let h = hubs.get(url); if (!h) { h = hub(url); hubs.set(url, h) } return h }

/** Global spine stream: a `thread` frame for every thread whose row changed. */
export const spineHub = () => shared(`${CHAT_BASE}/events`)
export const threadHub = (ref: string) => shared(`${CHAT_BASE}/threads/${encodeURIComponent(ref)}/events`)

/** Subscribe to one thread's frames while mounted. */
export function useThreadFrames(ref: string | undefined, onFrame: Listener, enabled = true) {
  const cb = useRef(onFrame)
  cb.current = onFrame
  useEffect(() => {
    if (!ref || !enabled) return
    return threadHub(ref).subscribe((f) => cb.current(f))
  }, [ref, enabled])
}

export const __test = { hub }

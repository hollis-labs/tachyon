import { useSyncExternalStore } from 'react'
import { hostFetch } from '@tachyon/host-sdk'
import { parseConsoleEvent } from '../events/parse'
import type { ConsoleEvent } from '../events/types'

const BASE = '/api/plugins/console/v1/tether'
export async function observationJSON<T>(path: string): Promise<T> {
  const r = await hostFetch(BASE + path, { headers: { 'Cache-Control': 'no-cache', Accept: 'application/json' } })
  if (!r.ok) throw Object.assign(new Error(`Activity unavailable (${r.status})`), { status: r.status })
  if (['stale', 'cached', 'refreshing'].includes(r.headers.get('X-Tachyon-Cache') ?? '')) throw new Error('Activity refresh unavailable')
  return r.json() as Promise<T>
}

interface EventRow { seq: number; at: string; kind: string; session_id?: string; payload_json?: string }
interface Page { events: EventRow[]; next_cursor?: number }
export interface Observations { events: ConsoleEvent[]; checkedAt: number; error: string | null; limited: boolean }

export function eventFromRow(row: EventRow): ConsoleEvent | null {
  if (!Number.isSafeInteger(row.seq) || !Number.isFinite(Date.parse(row.at))) return null
  let p: Record<string, unknown> = {}
  try { const value = JSON.parse(row.payload_json || '{}'); if (value && typeof value === 'object' && !Array.isArray(value)) p = value } catch { /* metadata may be absent */ }
  if (row.kind === 'session.state_changed' && p.from === p.to) return null // heartbeat is not work
  const session = row.session_id || (typeof p.session_id === 'string' ? p.session_id : undefined)
  const message = typeof p.message_id === 'string' ? p.message_id : undefined
  return parseConsoleEvent({ id: `tether:${row.seq}`, ts: Date.parse(row.at), kind: row.kind,
    source: { id: 'tether' }, session_id: session, payload: p,
    severity: row.kind.includes('failed') || row.kind.includes('dead_letter') || p.ok === false ? 'error' : 'info',
    subjects: session ? [{ type: 'session', id: session }] : message ? [{ type: 'message', id: message }] : [],
    thread_id: p.thread_id, cursor: String(row.seq),
  })
}

/** One bounded polling loop shared by chat, mail and the activity feed. */
export function createObservationStore(fetchPage = (query: string) => observationJSON<Page>('/events' + query)) {
  let state: Observations = { events: [], checkedAt: 0, error: null, limited: false }
  let high = 0
  let snapshotAt = 0
  let running: Promise<void> | null = null
  let timer: ReturnType<typeof setInterval> | undefined
  const listeners = new Set<() => void>()
  const refresh = (): Promise<void> => {
    if (running) return running
    running = (async () => {
      try {
        let cursor = 0
        // Periodic recent snapshots recover from sequence resets and missed windows.
        const since = Date.now() - snapshotAt < 60_000 ? high : 0
        let nextHigh = since
        const rows: EventRow[] = []
        // Initial snapshot is a recent window; incremental replay drains up to
        // three pages. A busy producer cannot cause an unbounded browser loop.
        for (let page = 0; page < 3; page++) {
          const q = new URLSearchParams()
          if (since) q.set('since_seq', String(since))
          if (cursor) q.set('cursor', String(cursor))
          const body = await fetchPage('?' + q)
          if (!Array.isArray(body.events)) throw new Error('Invalid activity response')
          rows.push(...body.events)
          for (const e of body.events) if (Number.isSafeInteger(e.seq)) nextHigh = Math.max(nextHigh, e.seq)
          const next = body.next_cursor || 0
          if (!next || (cursor && next >= cursor)) { cursor = 0; break }
          cursor = next
          if (!since) break
        }
        if (!since) snapshotAt = Date.now()
        if (nextHigh < high) state = { ...state, events: [] }
        high = nextHigh
        const all = new Map(state.events.map(e => [e.id, e]))
        rows.forEach(r => { const e = eventFromRow(r); if (e) all.set(e.id, e) })
        state = { events: [...all.values()].sort((a,b) => b.ts - a.ts || Number(b.cursor) - Number(a.cursor)).slice(0, 600), checkedAt: Date.now(), error: null, limited: !!cursor }
      } catch (e) {
        state = { ...state, error: e instanceof Error ? e.message : 'Activity unavailable' }
      } finally { running = null; listeners.forEach(f => f()) }
    })()
    return running
  }
  const sync = () => { if (typeof document === 'undefined' || document.visibilityState !== 'hidden') void refresh() }
  return {
    getSnapshot: () => state,
    refresh,
    subscribe(fn: () => void) {
      listeners.add(fn)
      if (listeners.size === 1) {
        sync()
        timer = setInterval(sync, 5000)
        window.addEventListener('focus', sync)
        document.addEventListener('visibilitychange', sync)
      }
      return () => {
        listeners.delete(fn)
        if (!listeners.size) { clearInterval(timer); window.removeEventListener('focus', sync); document.removeEventListener('visibilitychange', sync) }
      }
    },
  }
}
export const observations = createObservationStore()
export function useObservations() { return useSyncExternalStore(observations.subscribe, observations.getSnapshot, observations.getSnapshot) }

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
/** Latest hub work state per running session, kept apart from the capped event window so chat floods cannot evict it. */
export interface WorkState { working: boolean; at: number; seq: number }
export interface Observations { events: ConsoleEvent[]; checkedAt: number; error: string | null; limited: boolean; workStates: Record<string, WorkState> }
/** Per-session reads: a global newest-N list is dominated by chat agents, so each running session is asked for itself. */
export interface SessionSource { running(): Promise<string[]>; page(query: string): Promise<Page> }
const MAX_SESSIONS = 60
const SESSION_CONCURRENCY = 6
const SESSION_EVENTS = 25
export const defaultSessionSource: SessionSource = {
  async running() {
    const ids: string[] = []
    let cursor = ''
    const seen = new Set<string>()
    do {
      const body = await observationJSON<{ sessions?: { id?: string; state?: string }[]; next_cursor?: string }>('/sessions' + (cursor ? `?cursor=${encodeURIComponent(cursor)}` : ''))
      if (!Array.isArray(body.sessions)) throw new Error('Invalid session list')
      for (const x of body.sessions) if (x.id && x.state === 'running') ids.push(x.id)
      cursor = body.next_cursor ?? ''
      if (cursor && seen.has(cursor)) break
      seen.add(cursor)
    } while (cursor && ids.length < MAX_SESSIONS)
    return ids
  },
  page: query => observationJSON<Page>('/events' + query),
}

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
export function createObservationStore(fetchPage = (query: string) => observationJSON<Page>('/events' + query), sessions?: SessionSource) {
  let state: Observations = { events: [], checkedAt: 0, error: null, limited: false, workStates: {} }
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
        const workStates = sessions ? await observeSessions(sessions, rows, all, state.workStates) : state.workStates
        state = { workStates, events: [...all.values()].sort((a,b) => b.ts - a.ts || Number(b.cursor) - Number(a.cursor)).slice(0, 600), checkedAt: Date.now(), error: null, limited: !!cursor }
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

/** Work state of a row, or null. The hub emits it for every session, including claude-stream ones that emit no tool events. */
function workStateOf(row: EventRow): { working: boolean; at: number } | null {
  if (row.kind !== 'session.work_state_changed') return null
  const at = Date.parse(row.at)
  if (!Number.isFinite(at)) return null
  try {
    const to = JSON.parse(row.payload_json || '{}')?.to
    return to === 'processing' ? { working: true, at } : to === 'idle' ? { working: false, at } : null
  } catch { return null }
}

/**
 * Asks the hub about each running session separately (a short recent window plus its newest work-state change)
 * and merges the rows into `all`. A session that fails to answer keeps its last known state; sessions that are
 * no longer running are dropped. Failure here never blanks the global feed.
 */
async function observeSessions(source: SessionSource, globalRows: EventRow[], all: Map<string, ConsoleEvent>, previous: Record<string, WorkState>) {
  let ids: string[]
  try { ids = await source.running() } catch { return previous }
  const next: Record<string, WorkState> = {}
  for (const id of ids) if (previous[id]) next[id] = previous[id]
  const note = (id: string, row: EventRow) => {
    const w = workStateOf(row)
    const old = next[id]
    if (w && (!old || w.at > old.at || (w.at === old.at && row.seq > old.seq))) next[id] = { ...w, seq: row.seq }
  }
  for (const r of globalRows) if (r.session_id && ids.includes(r.session_id)) note(r.session_id, r)
  const q = (id: string, limit: number, kind?: string) => '?' + new URLSearchParams({ session_id: id, limit: String(limit), ...(kind ? { kind } : {}) })
  const queue = ids.slice(0, MAX_SESSIONS)
  await Promise.all(Array.from({ length: Math.min(SESSION_CONCURRENCY, queue.length) }, async () => {
    for (let id = queue.shift(); id; id = queue.shift()) {
      const [recent, work] = await Promise.allSettled([source.page(q(id, SESSION_EVENTS)), source.page(q(id, 1, 'session.work_state_changed'))])
      for (const result of [recent, work]) {
        if (result.status !== 'fulfilled' || !Array.isArray(result.value.events)) continue
        for (const row of result.value.events) {
          const e = eventFromRow(row)
          if (e) all.set(e.id, e)
          note(id, { ...row, session_id: id })
        }
      }
    }
  }))
  return next
}
export const observations = createObservationStore(undefined, defaultSessionSource)
export function useObservations() { return useSyncExternalStore(observations.subscribe, observations.getSnapshot, observations.getSnapshot) }

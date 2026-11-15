import { useSyncExternalStore } from 'react'
import { hostFetch } from '@tachyon/host-sdk'
import { parseConsoleEvent } from '../events/parse'
import type { ConsoleEvent } from '../events/types'

const BASE = '/api/plugins/console/v1/tether'
export async function observationJSON<T>(path: string, signal?: AbortSignal): Promise<T> {
  const r = await hostFetch(BASE + path, { headers: { 'Cache-Control': 'no-cache', Accept: 'application/json' }, signal })
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
export interface SessionSource { running(signal?: AbortSignal): Promise<string[]>; page(query: string, signal?: AbortSignal): Promise<Page> }
const MAX_SESSIONS = 60
const SESSION_CONCURRENCY = 6
const SESSION_EVENTS = 25
/** A stalled per-session read is cancelled after this long; the refresh never waits longer for it. */
export const SESSION_DEADLINE_MS = 4000
/** The whole session pass is abandoned after this, or sooner when the next poll supersedes it. */
export const SESSION_BUDGET_MS = 4500
export const defaultSessionSource: SessionSource = {
  async running(signal) {
    const ids: string[] = []
    let cursor = ''
    const seen = new Set<string>()
    do {
      const body = await observationJSON<{ sessions?: { id?: string; state?: string }[]; next_cursor?: string }>('/sessions' + (cursor ? `?cursor=${encodeURIComponent(cursor)}` : ''), signal)
      if (!Array.isArray(body.sessions)) throw new Error('Invalid session list')
      for (const x of body.sessions) if (x.id && x.state === 'running') ids.push(x.id)
      cursor = body.next_cursor ?? ''
      if (cursor && seen.has(cursor)) break
      seen.add(cursor)
    } while (cursor && ids.length < MAX_SESSIONS)
    return ids
  },
  page: (query, signal) => observationJSON<Page>('/events' + query, signal),
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
  let running: Promise<void> | null = null // the global pass only: session work never holds the next poll
  let latest: Promise<void> = Promise.resolve()
  let pass: AbortController | null = null
  let generation = 0 // a read that returns after a newer refresh began is dropped
  let timer: ReturnType<typeof setInterval> | undefined
  const listeners = new Set<() => void>()
  const refresh = (): Promise<void> => {
    if (running) return latest
    const mine = ++generation
    pass?.abort() // supersede: the previous session pass is cancelled and its remaining jobs discarded
    const ctl = pass = new AbortController()
    const publish = () => listeners.forEach(f => f())
    const globalPass = (async (): Promise<EventRow[] | null> => {
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
        state = { ...state, events: [...all.values()].sort((a,b) => b.ts - a.ts || Number(b.cursor) - Number(a.cursor)).slice(0, 600), checkedAt: Date.now(), error: null, limited: !!cursor }
        publish() // the global feed is visible before any per-session read starts
        return rows
      } catch (e) {
        state = { ...state, error: e instanceof Error ? e.message : 'Activity unavailable' }
        return null
      } finally { running = null; publish() }
    })()
    running = globalPass.then(() => undefined)
    const sessionPass = globalPass.then(rows => rows && sessions ? observeSessions(sessions, rows, mine, ctl) : undefined)
    return latest = Promise.all([running, sessionPass]).then(() => undefined)
  }
  /** Merge rows from one session read into the shared state, newest-first and capped, and notify at once. */
  const apply = (id: string, rows: EventRow[], mine: number) => {
    if (mine !== generation) return // superseded: discard
    const all = new Map(state.events.map(e => [e.id, e]))
    const workStates = { ...state.workStates }
    for (const row of rows) {
      const e = eventFromRow(row)
      if (e) all.set(e.id, e)
      const w = workStateOf(row), old = workStates[id]
      if (w && (!old || w.at > old.at || (w.at === old.at && row.seq > old.seq))) workStates[id] = { ...w, seq: row.seq }
    }
    state = { ...state, workStates, events: [...all.values()].sort((a,b) => b.ts - a.ts || Number(b.cursor) - Number(a.cursor)).slice(0, 600) }
    listeners.forEach(f => f())
  }
  /**
   * Reads each running session separately (recent window + newest work-state change) with 6 requests in flight in
   * total, each under its own deadline, publishing every result as it lands. A slow or failed read affects only
   * that session: it keeps its last state, and the refresh ends by the deadline.
   */
  async function observeSessions(source: SessionSource, globalRows: EventRow[], mine: number, ctl: AbortController) {
    const budget = setTimeout(() => ctl.abort(), SESSION_BUDGET_MS)
    try { await observeWithin(source, globalRows, mine, ctl.signal) } finally { clearTimeout(budget) }
  }
  async function observeWithin(source: SessionSource, globalRows: EventRow[], mine: number, stop: AbortSignal) {
    let ids: string[]
    try { ids = (await withDeadline(signal => source.running(signal), stop)).slice(0, MAX_SESSIONS) } catch { return }
    if (mine !== generation || stop.aborted) return
    const live = new Set(ids)
    const kept = Object.fromEntries(Object.entries(state.workStates).filter(([id]) => live.has(id)))
    state = { ...state, workStates: kept } // stopped sessions drop out
    listeners.forEach(f => f())
    for (const id of ids) { const own = globalRows.filter(r => r.session_id === id); if (own.length) apply(id, own, mine) }
    const q = (id: string, limit: number, kind?: string) => '?' + new URLSearchParams({ session_id: id, limit: String(limit), ...(kind ? { kind } : {}) })
    // Start each pass at a different session so stuck ones at the front cannot starve the rest every time.
    const start = ids.length ? mine % ids.length : 0
    const jobs = [...ids.slice(start), ...ids.slice(0, start)].flatMap(id => [{ id, query: q(id, SESSION_EVENTS) }, { id, query: q(id, 1, 'session.work_state_changed') }])
    await Promise.all(Array.from({ length: Math.min(SESSION_CONCURRENCY, jobs.length) }, async () => {
      for (let job = jobs.shift(); job && !stop.aborted; job = jobs.shift()) {
        try {
          const page = await withDeadline(signal => source.page(job.query, signal), stop)
          if (Array.isArray(page.events)) apply(job.id, page.events.map(r => ({ ...r, session_id: job.id })), mine)
        } catch { /* keep this session's last observation */ }
      }
    }))
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

/** Runs a read under a deadline and the pass's stop signal; it rejects on time even if the source ignores its signal. */
function withDeadline<T>(read: (signal: AbortSignal) => Promise<T>, stop: AbortSignal): Promise<T> {
  const ctl = new AbortController()
  return new Promise<T>((resolve, reject) => {
    const cancel = () => { ctl.abort(); reject(new Error('Session read cancelled')) }
    if (stop.aborted) return cancel()
    const timer = setTimeout(cancel, SESSION_DEADLINE_MS)
    stop.addEventListener('abort', cancel, { once: true })
    read(ctl.signal).then(resolve, reject).finally(() => { clearTimeout(timer); stop.removeEventListener('abort', cancel) })
  })
}
export const observations = createObservationStore(undefined, defaultSessionSource)
export function useObservations() { return useSyncExternalStore(observations.subscribe, observations.getSnapshot, observations.getSnapshot) }

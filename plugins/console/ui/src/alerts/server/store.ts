// External store for server alerts: one list/counts load, then the SSE stream keeps it current.
// Ops are optimistic and send expected_version; a conflict adopts the server's copy.

import { AlertsApiError, isVersionConflict, type AlertsClient } from './client'
import type { ServerAlert, ServerCounts, ServerFrame, ServerOp } from './types'

export type Connection = 'connecting' | 'live' | 'reconnecting' | 'down'

export interface ServerAlertsSnapshot {
  alerts: ServerAlert[]
  counts: ServerCounts | null
  ready: boolean
  connection: Connection
  /** serverNow - deviceNow, so countdowns follow the server clock. */
  skewMs: number
  /** Last failed write, plain text; cleared by the next successful one. */
  error: string | null
  /** Sources the server reports as down, by name ("torque"). */
  sourcesDown: string[]
}

type StreamFactory = (url: string) => EventSource

const byRecent = (a: ServerAlert, b: ServerAlert) => b.last_at_ms - a.last_at_ms || (a.alert_id < b.alert_id ? 1 : -1)

export class ServerAlertsStore {
  private snap: ServerAlertsSnapshot = { alerts: [], counts: null, ready: false, connection: 'connecting', skewMs: 0, error: null, sourcesDown: [] }
  private byId = new Map<string, ServerAlert>()
  private seq = 0
  private es: EventSource | null = null
  private listeners = new Set<() => void>()
  private toastListeners = new Set<(a: ServerAlert) => void>()
  private prefsListeners = new Set<() => void>()
  private closed = false
  private loading: Promise<void> | null = null

  constructor(
    private client: AlertsClient,
    private makeStream: StreamFactory = (u) => new EventSource(u),
  ) {}

  getSnapshot = () => this.snap
  subscribe = (l: () => void) => {
    this.listeners.add(l)
    return () => void this.listeners.delete(l)
  }
  /** Called for a frame the server marked `toast`. */
  onToast = (l: (a: ServerAlert) => void) => {
    this.toastListeners.add(l)
    return () => void this.toastListeners.delete(l)
  }
  onPrefsChanged = (l: () => void) => {
    this.prefsListeners.add(l)
    return () => void this.prefsListeners.delete(l)
  }

  private publish(patch: Partial<ServerAlertsSnapshot>) {
    const alerts = patch.alerts ?? this.snap.alerts
    this.snap = { ...this.snap, ...patch, alerts }
    this.listeners.forEach((l) => l())
  }
  private flush(patch: Partial<ServerAlertsSnapshot> = {}) {
    this.publish({ alerts: [...this.byId.values()].sort(byRecent), ...patch })
  }

  /** Initial load and resync: active and recent history, then counts. */
  load(): Promise<void> {
    this.loading ??= (async () => {
      try {
        const [active, history] = await Promise.all([this.client.list('active', { limit: 200 }), this.client.list('history', { limit: 50 })])
        this.byId = new Map([...history.alerts, ...active.alerts].map((a) => [a.alert_id, a]))
        this.seq = Math.max(active.seq, history.seq)
        this.flush({ counts: active.counts, ready: true, skewMs: active.server_now_ms - Date.now(), error: null })
      } catch (e) {
        this.publish({ ready: true, connection: 'down', error: e instanceof Error ? e.message : 'Could not load alerts' })
      } finally {
        this.loading = null
      }
    })()
    return this.loading
  }

  /** Ingest health: which sources have stopped delivering. A failed check leaves the last answer. */
  async refreshSources() {
    try {
      const r = await this.client.sources()
      const down = r.sources.filter((x) => x.state === 'down').map((x) => x.source)
      if (down.join() !== this.snap.sourcesDown.join()) this.publish({ sourcesDown: down })
    } catch {
      /* keep the last answer */
    }
  }

  start(): () => void {
    this.closed = false
    void this.load().then(() => this.open())
    void this.refreshSources()
    const poll = setInterval(() => void this.refreshSources(), 60_000)
    return () => {
      clearInterval(poll)
      this.closed = true
      this.es?.close()
      this.es = null
    }
  }

  /** Reconnect by hand after the stream gave up: refetch, then open a fresh stream from the new cursor. */
  async retry() {
    this.es?.close()
    this.es = null
    this.publish({ connection: 'connecting' })
    await this.load()
    void this.refreshSources()
    if (this.snap.connection !== 'down') this.open()
  }

  private open() {
    if (this.closed || typeof EventSource === 'undefined') return
    const es = this.makeStream(this.client.streamUrl(this.seq || undefined))
    this.es = es
    es.onopen = () => this.publish({ connection: 'live' })
    es.onerror = () => {
      // The browser retries by itself, resending Last-Event-ID.
      this.publish({ connection: es.readyState === 2 ? 'down' : 'reconnecting' })
    }
    es.onmessage = (m) => {
      try {
        this.apply(JSON.parse(m.data) as ServerFrame)
      } catch {
        /* a malformed frame is skipped */
      }
    }
  }

  apply(f: ServerFrame) {
    switch (f.type) {
      case 'resync':
        void this.load()
        return
      case 'counts':
        if (f.counts) this.publish({ counts: f.counts })
        return
      case 'prefs':
        this.prefsListeners.forEach((l) => l())
        return
      case 'alert.removed':
      case 'alert.cleared': {
        const id = f.alert?.alert_id ?? f.alert_id
        if (id && this.byId.delete(id)) this.flush()
        return
      }
      default: {
        const a = f.alert
        if (!a) return
        const have = this.byId.get(a.alert_id)
        if (have && have.version > a.version) return // an older frame replayed after a newer write
        this.byId.set(a.alert_id, a)
        this.seq = Math.max(this.seq, a.seq)
        this.flush()
        if (f.toast) this.toastListeners.forEach((l) => l(a))
      }
    }
  }

  /** Apply an op optimistically; on failure restore or adopt the server's copy and surface a message. */
  async op(id: string, op: ServerOp, optimistic: (a: ServerAlert) => ServerAlert): Promise<boolean> {
    const before = this.byId.get(id)
    if (!before) return false
    this.byId.set(id, optimistic(before))
    this.flush()
    try {
      const next = await this.client.op(id, op, before.version)
      this.byId.set(id, next)
      this.flush({ error: null })
      return true
    } catch (e) {
      if (isVersionConflict(e) && e.current) this.byId.set(id, e.current)
      else this.byId.set(id, before)
      this.flush({ error: e instanceof AlertsApiError && e.code === 'version_conflict' ? 'This alert changed on another device. Showing the latest.' : 'That did not save. Try again.' })
      return false
    }
  }

  async bulk(op: 'read' | 'dismiss', ids: string[], optimistic: (a: ServerAlert) => ServerAlert) {
    const before = new Map(ids.map((i) => [i, this.byId.get(i)] as const))
    for (const [i, a] of before) if (a) this.byId.set(i, optimistic(a))
    this.flush()
    try {
      const r = await this.client.bulk(op, { ids })
      this.flush({ counts: r.counts, error: null })
      return true
    } catch {
      for (const [i, a] of before) if (a) this.byId.set(i, a)
      this.flush({ error: 'That did not save. Try again.' })
      return false
    }
  }
}

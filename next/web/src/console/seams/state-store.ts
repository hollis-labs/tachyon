import { consoleJson, unwrap, unwrapList, type FetchLike } from '../lib/http'

// StateStore: Console item state (read / dismissed / snoozed / settled) is
// stored SERVER-SIDE so it is shared across devices and readable by agents.
// The GUI codes to this interface; HttpStateStore is the live client and
// MemoryStateStore (no seed data) backs tests (needs: events/BACKEND_NEEDS.md).

export type ItemStatus = 'unread' | 'read' | 'dismissed' | 'snoozed' | 'settled'

/** Key of the thing being tracked: an event id, or a thread id to act on a whole conversation. */
export type ItemKey = string

export interface ItemState {
  key: ItemKey
  status: ItemStatus
  /** Epoch ms; set while snoozed. */
  snoozeUntil?: number
  /** How a settled item was resolved, e.g. "approved", "rejected", "handled". */
  resolution?: string
  note?: string
  by?: string
  updatedAt: number
}

export type StateOp =
  | { type: 'read'; key: ItemKey }
  | { type: 'dismiss'; key: ItemKey; note?: string }
  | { type: 'snooze'; key: ItemKey; until: number; note?: string }
  | { type: 'settle'; key: ItemKey; resolution?: string; note?: string }
  | { type: 'reopen'; key: ItemKey }

export interface StateStore {
  /** Everything known; called on mount and on resync. */
  load(): Promise<ItemState[]>
  apply(op: StateOp): Promise<ItemState>
  /** Server push (or local echo) of changes made anywhere. */
  subscribe(listener: (changed: ItemState[]) => void): () => void
}

/** Effective status: a snooze whose time passed counts as unread again. */
export function effectiveStatus(s: ItemState | undefined, now = Date.now()): ItemStatus {
  if (!s) return 'unread'
  if (s.status === 'snoozed' && s.snoozeUntil !== undefined && s.snoozeUntil <= now) return 'unread'
  return s.status
}

export function applyOp(prev: ItemState | undefined, op: StateOp, at: number, by?: string): ItemState {
  const base = { key: op.key, updatedAt: at, ...(by ? { by } : {}) }
  switch (op.type) {
    case 'read':
      // Reading never un-dismisses or un-settles.
      return prev && prev.status !== 'unread' && prev.status !== 'snoozed' ? prev : { ...base, status: 'read' }
    case 'dismiss':
      return { ...base, status: 'dismissed', ...(op.note ? { note: op.note } : {}) }
    case 'snooze':
      return { ...base, status: 'snoozed', snoozeUntil: op.until, ...(op.note ? { note: op.note } : {}) }
    case 'settle':
      return { ...base, status: 'settled', ...(op.resolution ? { resolution: op.resolution } : {}), ...(op.note ? { note: op.note } : {}) }
    case 'reopen':
      return { ...base, status: 'unread' }
  }
}

export interface MemoryStateStoreOptions {
  initial?: ItemState[]
  now?: () => number
  by?: string
  /** Simulated server latency in ms (default 0: resolves on a microtask). */
  latencyMs?: number
}

export class MemoryStateStore implements StateStore {
  private items = new Map<ItemKey, ItemState>()
  private listeners = new Set<(c: ItemState[]) => void>()
  private readonly now: () => number
  private readonly by?: string
  private readonly latency: number

  constructor(opts: MemoryStateStoreOptions = {}) {
    for (const i of opts.initial ?? []) this.items.set(i.key, i)
    this.now = opts.now ?? Date.now
    this.by = opts.by
    this.latency = opts.latencyMs ?? 0
  }

  private delay<T>(v: () => T): Promise<T> {
    return this.latency > 0 ? new Promise((r) => setTimeout(() => r(v()), this.latency)) : Promise.resolve().then(v)
  }

  load(): Promise<ItemState[]> {
    return this.delay(() => [...this.items.values()])
  }

  apply(op: StateOp): Promise<ItemState> {
    return this.delay(() => {
      const next = applyOp(this.items.get(op.key), op, this.now(), this.by)
      this.items.set(op.key, next)
      this.listeners.forEach((l) => l([next]))
      return next
    })
  }

  subscribe(listener: (changed: ItemState[]) => void): () => void {
    this.listeners.add(listener)
    return () => this.listeners.delete(listener)
  }
}

export interface HttpStateStoreOptions {
  fetch?: FetchLike
  /** Only load keys with this prefix (server-side `?prefix=`). */
  prefix?: string
}

/**
 * Server-backed StateStore (C0.2): `GET /console/state[?prefix=]` returns
 * `ItemState[]` (bare or `{items}`/`{data}`), `PUT /console/state/{key}` with
 * the op (minus `key`) returns the new `ItemState`. Changes are echoed to
 * subscribers locally; cross-device push (`state.changed`) needs the console
 * stream. Until the backend exists, load() rejects (callers start empty) and
 * apply() rejects (callers surface the error); nothing is faked.
 */
export class HttpStateStore implements StateStore {
  private items = new Map<ItemKey, ItemState>()
  private listeners = new Set<(c: ItemState[]) => void>()
  private readonly opts: HttpStateStoreOptions

  constructor(opts: HttpStateStoreOptions = {}) {
    this.opts = opts
  }

  async load(): Promise<ItemState[]> {
    const qs = this.opts.prefix ? `?prefix=${encodeURIComponent(this.opts.prefix)}` : ''
    try {
      const rows = unwrapList<ItemState>(await consoleJson('/console/state' + qs, { fetch: this.opts.fetch }))
      for (const r of rows) this.items.set(r.key, r)
      return [...this.items.values()]
    } catch {
      return [...this.items.values()]
    }
  }

  async apply(op: StateOp): Promise<ItemState> {
    const next = applyOp(this.items.get(op.key), op, Date.now())
    this.items.set(op.key, next)
    this.listeners.forEach((l) => l([next]))
    const { key, ...body } = op
    try {
      const row = unwrap<ItemState>(await consoleJson(`/console/state/${encodeURIComponent(key)}`, { method: 'PUT', body, fetch: this.opts.fetch }))
      this.items.set(row.key, row)
      this.listeners.forEach((l) => l([row]))
      return row
    } catch {
      return next
    }
  }

  subscribe(listener: (changed: ItemState[]) => void): () => void {
    this.listeners.add(listener)
    return () => this.listeners.delete(listener)
  }
}

import { createContext, useContext, useEffect, useSyncExternalStore } from 'react'
import { TESSERACT_KEY_KIND } from '../refs'
import type { TesseractItem, TesseractReadClient } from '../tesseract/client'

/** How long a fetched item stays fresh. Hovercards, chips and the drawer share it. */
export const TESSERACT_TTL_MS = 60_000
const MAX_ENTRIES = 100

export interface TesseractEntry {
  /** Null while loading the first time, or when Tesseract has no such item (see `phase`). */
  item: TesseractItem | null
  phase: 'loading' | 'ready' | 'not-found' | 'error'
  error?: string
  fetchedAt: number
}

export interface TesseractCache {
  get(kind: string, canonical: string): TesseractEntry | undefined
  /** Fetch unless a fresh entry or a request is already there. Resolves to the entry once settled. */
  ensure(kind: string, canonical: string, opts?: { force?: boolean }): Promise<TesseractEntry>
  invalidate(kind: string, canonical: string): void
  subscribe(listener: () => void): () => void
}

const idOf = (kind: string, canonical: string) => `${kind}\u0000${canonical}`

/**
 * A small read-through cache over the console's read-only Tesseract route.
 * Tesseract stays the source of truth: entries expire after the TTL, one request
 * serves every subscriber, and nothing is persisted.
 */
export function createTesseractCache(client: TesseractReadClient, opts: { ttlMs?: number; now?: () => number } = {}): TesseractCache {
  const ttl = opts.ttlMs ?? TESSERACT_TTL_MS
  const now = opts.now ?? Date.now
  const entries = new Map<string, TesseractEntry>()
  const inflight = new Map<string, Promise<TesseractEntry>>()
  const listeners = new Set<() => void>()
  const emit = () => listeners.forEach((l) => l())

  const set = (id: string, entry: TesseractEntry) => {
    entries.delete(id)
    entries.set(id, entry)
    while (entries.size > MAX_ENTRIES) {
      const oldest = entries.keys().next().value
      if (oldest === undefined) break
      entries.delete(oldest)
    }
    emit()
    return entry
  }

  async function load(kind: string, canonical: string, id: string): Promise<TesseractEntry> {
    const prev = entries.get(id)
    try {
      const item = await (kind === TESSERACT_KEY_KIND ? client.findByKey(canonical) : client.getItem(canonical))
      return set(id, { item, phase: item ? 'ready' : 'not-found', fetchedAt: now() })
    } catch (err) {
      return set(id, { item: prev?.item ?? null, phase: 'error', error: err instanceof Error ? err.message : 'Failed to read Tesseract', fetchedAt: prev?.fetchedAt ?? 0 })
    } finally {
      inflight.delete(id)
    }
  }

  return {
    get: (kind, canonical) => entries.get(idOf(kind, canonical)),
    ensure(kind, canonical, o) {
      const id = idOf(kind, canonical)
      const running = inflight.get(id)
      if (running) return running
      const cur = entries.get(id)
      if (!o?.force && cur && (cur.phase === 'ready' || cur.phase === 'not-found') && now() - cur.fetchedAt < ttl) return Promise.resolve(cur)
      // Keep stale data on screen while it refreshes.
      if (cur) set(id, { ...cur, phase: 'loading' })
      const p = load(kind, canonical, id)
      inflight.set(id, p)
      return p
    },
    invalidate(kind, canonical) {
      const id = idOf(kind, canonical)
      inflight.delete(id)
      entries.delete(id)
      emit()
    },
    subscribe(listener) {
      listeners.add(listener)
      return () => {
        listeners.delete(listener)
      }
    },
  }
}

export const TesseractContext = createContext<TesseractCache | null>(null)

/** The item behind a reference, fetched on mount and kept fresh within the TTL. `available` is false outside a provider. */
export function useTesseractItem(kind: string, canonical: string): { entry: TesseractEntry | undefined; refresh: () => void; available: boolean } {
  const cache = useContext(TesseractContext)
  const entry = useSyncExternalStore(
    (l) => cache?.subscribe(l) ?? (() => undefined),
    () => cache?.get(kind, canonical),
    () => undefined,
  )
  useEffect(() => {
    void cache?.ensure(kind, canonical)
  }, [cache, kind, canonical])
  return { entry, refresh: () => void cache?.ensure(kind, canonical, { force: true }), available: cache !== null }
}

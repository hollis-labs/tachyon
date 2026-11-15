import { dataRefreshRegistry, refreshPageData } from "@tachyon/host-sdk"
import { useCallback, useContext, useEffect, useState } from 'react'
import { ItemStateContext } from '../../events/context'
import type { ForgeClient } from '../client'
import type { PendingPrSummary } from '../types'

export interface PendingPrsState {
  prs: PendingPrSummary[]
  loading: boolean
  error: string | null
  refreshedAt: number | null
  /** The forge's own last refresh (ms) when it reports one; else null. */
  forgeRefreshedAt: number | null
  /** The forge is answering from cache (degraded). */
  cached: boolean
  refresh: () => Promise<void>
}

/** Events on the forge stream that change the pending list. */
const LIST_EVENTS = ['pr.updated', 'pr.merged', 'resync'] as const
/** A burst of stream events is one reload. */
export const STREAM_RELOAD_MS = 1500

/**
 * Loads ForgeClient.listPendingPrs(). With `streamUrl` it reloads when the forge stream reports a change and polls
 * (every `pollMs`) only while the stream is down; without it, it polls every `pollMs` (0 = never).
 */
export function usePendingPrs(client: ForgeClient, opts: { pollMs?: number; streamUrl?: string } = {}): PendingPrsState {
  const { pollMs = 0, streamUrl } = opts
  const [s, setS] = useState<Omit<PendingPrsState, 'refresh'>>({ prs: [], loading: true, error: null, refreshedAt: null, forgeRefreshedAt: null, cached: false })
  const load = useCallback(async () => {
    try {
      const prs = await client.listPendingPrs()
      const st = await client.getStatus?.().catch(() => null)
      setS({ prs, loading: false, error: null, refreshedAt: Date.now(), forgeRefreshedAt: st?.lastRefreshMs ?? null, cached: st?.degraded === true })
    } catch (e) {
      setS((p) => ({ ...p, loading: false, error: e instanceof Error ? e.message : 'Could not load pending PRs' }))
    }
  }, [client])
  useEffect(() => {
    void load()
    const off = dataRefreshRegistry('forge').subscribe(() => void load())
    let t: ReturnType<typeof setInterval> | null = null
    const poll = (on: boolean) => {
      if (t) clearInterval(t)
      t = on && pollMs > 0 ? setInterval(() => void load(), pollMs) : null
    }
    poll(true)
    let es: EventSource | null = null
    let pending: ReturnType<typeof setTimeout> | null = null
    if (streamUrl && typeof EventSource !== 'undefined') {
      es = new EventSource(streamUrl)
      const changed = () => {
        if (pending) return
        pending = setTimeout(() => { pending = null; void load() }, STREAM_RELOAD_MS)
      }
      let wasDown = false
      es.onopen = () => { poll(false); if (wasDown) void load(); wasDown = false }
      es.onerror = () => { wasDown = true; poll(true) }
      for (const type of LIST_EVENTS) es.addEventListener(type, changed)
    }
    return () => { off(); poll(false); es?.close(); if (pending) clearTimeout(pending) }
  }, [load, pollMs, streamUrl])
  return { ...s, refresh: async () => { await refreshPageData(); await load() } }
}

/** Server-backed item state mirror owned by <ConsoleProvider>. */
export function useItemStates() {
  return useContext(ItemStateContext)
}

/** Wall clock that ticks (default every 30s) so ages and snooze expiry stay current without impure renders. */
export function useNow(tickMs = 30_000): number {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    const t = setInterval(() => setNow(Date.now()), tickMs)
    return () => clearInterval(t)
  }, [tickMs])
  return now
}

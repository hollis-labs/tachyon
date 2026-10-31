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

/** Loads ForgeClient.listPendingPrs(), optionally polling. Opens no stream. */
export function usePendingPrs(client: ForgeClient, opts: { pollMs?: number } = {}): PendingPrsState {
  const { pollMs = 0 } = opts
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
    const t = pollMs > 0 ? setInterval(() => void load(), pollMs) : null
    return () => { off(); if (t) clearInterval(t) }
  }, [load, pollMs])
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

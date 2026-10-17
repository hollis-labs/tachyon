import { useSyncExternalStore } from "react"

export interface DataFreshness {
  url: string
  page: string
  scope: string
  state: "live" | "cached" | "stale" | "error"
  updatedAt: string | null
  freshSeconds: number
  problem: string | null
}

let snapshots: readonly DataFreshness[] = []
const listeners = new Set<() => void>()
const empty: readonly DataFreshness[] = []

function publish(value: DataFreshness) {
  snapshots = [...snapshots.filter((s) => s.url !== value.url || s.page !== value.page), value].slice(-200)
  for (const listener of listeners) listener()
}

export function responseFreshness(res: Response, url: string, page = ""): DataFreshness | null {
  const scope = res.headers.get("X-Tachyon-Source")
  const state = res.headers.get("X-Tachyon-Cache")
  if (!scope || !state || !["live", "cached", "stale", "error"].includes(state)) return null
  return { url, page, scope, state: state as DataFreshness["state"], updatedAt: res.headers.get("X-Tachyon-Updated-At"), freshSeconds: Number(res.headers.get("X-Tachyon-Fresh-Seconds") ?? 0), problem: res.headers.get("X-Tachyon-Cache-Error") }
}

/** Shared first-party fetch: observe host metadata without changing JSON shapes. */
export const hostFetch: typeof fetch = async (input, init) => {
  const url = typeof input === "string" ? input : input instanceof URL ? input.href : input.url
  const page = typeof window === "undefined" ? "" : window.location.pathname
  let options = init
  try {
    const method = init?.method ?? (input instanceof Request ? input.method : "GET")
    if (method === "GET" && Date.now() < Number(sessionStorage.getItem("tachyon:refresh-until"))) {
      const headers = new Headers(init?.headers ?? (input instanceof Request ? input.headers : undefined))
      headers.set("Cache-Control", "no-cache")
      options = { ...init, headers }
    }
  } catch { /* Browser storage may be disabled; normal reads still work. */ }
  try {
    const res = await globalThis.fetch(input, options)
    const metadata = responseFreshness(res, url, page)
    if (metadata) publish(metadata)
    return res
  } catch (error) {
    // Aborted navigation is not a connection failure.
    if (!(error instanceof Error && error.name === "AbortError")) {
      const prior = snapshots.find((s) => s.url === url && s.page === page)
      const scope = prior?.scope ?? (url.includes("/catalog/") ? "catalog" : url.includes("/activity/") ? "activity" : url.includes("/planner/") ? "planner" : url.includes("/forge/") ? "forge" : null)
      if (scope) publish({ url, page, scope, updatedAt: null, freshSeconds: 0, ...prior, state: "error", problem: "host_unreachable" })
    }
    throw error
  }
}

export function useDataFreshness() {
  return useSyncExternalStore(
    (listener) => { listeners.add(listener); return () => { listeners.delete(listener) } },
    () => snapshots,
    () => empty,
  )
}

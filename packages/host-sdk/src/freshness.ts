import { createRefreshRegistry, type RefreshRegistry } from "@hollis-labs/design-app-runtime"
import { useSyncExternalStore } from "react"

export interface DataFreshness {
  url: string
  page: string
  scope: string
  state: "live" | "cached" | "refreshing" | "stale" | "error" | "imported"
  updatedAt: string | null
  freshSeconds: number
  problem: string | null
}

let snapshots: readonly DataFreshness[] = []
const listeners = new Set<() => void>()
const empty: readonly DataFreshness[] = []
const registries = new Map<string, RefreshRegistry>()

/** Portfolio refresh registries let mounted consumers adopt completed reads. */
export function dataRefreshRegistry(scope: string): RefreshRegistry {
  let registry = registries.get(scope)
  if (!registry) { registry = createRefreshRegistry(); registries.set(scope, registry) }
  return registry
}

function publish(value: DataFreshness) {
  snapshots = [...snapshots.filter((s) => s.url !== value.url || s.page !== value.page), value].slice(-200)
  for (const listener of listeners) listener()
}

export function responseFreshness(res: Response, url: string, page = ""): DataFreshness | null {
  const scope = res.headers.get("X-Tachyon-Source")
  const state = res.headers.get("X-Tachyon-Cache")
  if (!scope || !state || !["live", "cached", "refreshing", "stale", "error", "imported"].includes(state)) return null
  return { url, page, scope, state: state as DataFreshness["state"], updatedAt: res.headers.get("X-Tachyon-Updated-At"), freshSeconds: Number(res.headers.get("X-Tachyon-Fresh-Seconds") ?? 0), problem: res.headers.get("X-Tachyon-Cache-Error") }
}

interface Snapshot {
  body: string
  headers: Headers
  status: number
  checkedAt: number
  bytes: number
}

// This is a bounded navigation accelerator, not persistent storage. Only
// host-admitted snapshots enter it; SQLite remains the durable source.
const bodies = new Map<string, Snapshot>()
const inFlight = new Map<string, Promise<Response>>()
let bytes = 0
let generation = 0
const MAX_BYTES = 16 << 20
const MAX_ENTRIES = 128

function forget(url: string) {
  const old = bodies.get(url)
  if (old) bytes -= old.bytes
  bodies.delete(url)
}

function forgetScope(scope: string) {
  generation++
  for (const [url, value] of bodies) if (value.headers.get("X-Tachyon-Source") === scope) forget(url)
}

function materialize(value: Snapshot, state?: string): Response {
  const headers = new Headers(value.headers)
  if (state) headers.set("X-Tachyon-Cache", state)
  return new Response(value.body, { status: value.status, headers })
}

function eligibleURL(input: string): boolean {
  try {
    const url = new URL(input, globalThis.location?.origin ?? "http://localhost")
    return url.origin === (globalThis.location?.origin ?? "http://localhost") && url.pathname.startsWith("/api/plugins/") && !url.pathname.includes("/stream/")
  } catch { return false }
}

async function network(url: string, init: RequestInit | undefined, force: boolean): Promise<Response> {
  const headers = new Headers(init?.headers)
  headers.set("Prefer", "stale-while-revalidate")
  if (force) headers.set("Cache-Control", "no-cache")
  const key = `${force ? "force" : "normal"}:${url}`
  const prior = inFlight.get(key)
  if (prior) return (await prior).clone()
  const epoch = generation
  const run = (async () => {
    const res = await globalThis.fetch(url, { ...init, headers, signal: AbortSignal.timeout(70_000) })
    const meta = responseFreshness(res, url)
    if (meta && meta.state === "error") {
      if (meta.problem === "authentication" || meta.problem === "configuration") forgetScope(meta.scope)
      else forget(url)
    }
    if (meta && res.headers.get("X-Tachyon-Cache-Enabled") !== "true") forget(url)
    if (meta && res.ok && meta.state !== "error" && res.headers.get("X-Tachyon-Cache-Enabled") === "true" && epoch === generation) {
      const body = await res.clone().text()
      if (epoch !== generation) return res
      const size = body.length * 2 // bounded JS string footprint
      forget(url)
      if (size <= MAX_BYTES) {
        bodies.set(url, { body, headers: new Headers(res.headers), status: res.status, checkedAt: Date.now(), bytes: size })
        bytes += size
        while (bodies.size > MAX_ENTRIES || bytes > MAX_BYTES) forget(bodies.keys().next().value as string)
      }
    }
    return res
  })()
  inFlight.set(key, run)
  try { return (await run).clone() } finally { inFlight.delete(key) }
}

function notifyResponse(res: Response, url: string, page: string) {
  const metadata = responseFreshness(res, url, page)
  if (metadata) publish(metadata)
  return metadata
}

// Deduplicate revalidation, including its notification, so concurrent consumers
// cannot produce refresh loops. Snapshot replacement preserves server ordering
// and removals; these endpoints do not offer a delta protocol.
const refreshing = new Map<string, Promise<void>>()
function revalidate(url: string, page: string, init?: RequestInit): Promise<void> {
  const prior = refreshing.get(url)
  if (prior) return prior
  const epoch = generation
  const run = (async () => {
    try {
      const res = await network(url, init, true)
      if (epoch !== generation && res.ok) return
      const metadata = notifyResponse(res, url, page)
      if (metadata) dataRefreshRegistry(metadata.scope).refresh()
    } catch {
      const prior = snapshots.find((s) => s.url === url)
      if (prior) publish({ ...prior, page, state: "stale", problem: "host_unreachable" })
      const saved = bodies.get(url)
      if (saved) {
        saved.checkedAt = Date.now()
        saved.headers.set("X-Tachyon-Cache", "stale")
        saved.headers.set("X-Tachyon-Cache-Error", "host_unreachable")
      }
    }
  })()
  refreshing.set(url, run)
  void run.finally(() => refreshing.delete(url))
  return run
}

function abortable<T>(promise: Promise<T>, signal?: AbortSignal | null): Promise<T> {
  if (!signal) return promise
  if (signal.aborted) return Promise.reject(new DOMException("Aborted", "AbortError"))
  return new Promise((resolve, reject) => {
    const abort = () => reject(new DOMException("Aborted", "AbortError"))
    signal.addEventListener("abort", abort, { once: true })
    promise.then(resolve, reject).finally(() => signal.removeEventListener("abort", abort))
  })
}

/** Cache-first reads, with the host's durable policy as the authority. */
export const hostFetch: typeof fetch = async (input, init) => {
  const url = typeof input === "string" ? input : input instanceof URL ? input.href : input.url
  const page = typeof window === "undefined" ? "" : window.location.pathname
  const method = init?.method ?? (input instanceof Request ? input.method : "GET")
  const headers = new Headers(init?.headers ?? (input instanceof Request ? input.headers : undefined))
  const signal = init?.signal ?? (input instanceof Request ? input.signal : undefined)
  const read = method === "GET" && eligibleURL(url) && !headers.has("Authorization") && !headers.get("Cache-Control")?.includes("no-store")
  const force = headers.get("Cache-Control")?.includes("no-cache") ?? false
  if (read && !force) {
    const saved = bodies.get(url)
    if (saved) {
      const updated = Date.parse(saved.headers.get("X-Tachyon-Updated-At") ?? "")
      const fresh = Number(saved.headers.get("X-Tachyon-Fresh-Seconds")) * 1000
      const swr = Number(saved.headers.get("X-Tachyon-Revalidate-Seconds")) * 1000
      const age = Date.now() - updated
      if (age >= 0 && age < fresh + swr) {
        const recentCheck = Date.now() - saved.checkedAt < 2000
        const problem = saved.headers.get("X-Tachyon-Cache-Error")
        const state = problem ? "stale" : saved.headers.get("X-Tachyon-Cache") === "imported" ? "imported" : age < fresh ? "cached" : "refreshing"
        const res = materialize(saved, state)
        notifyResponse(res, url, page)
        if (age >= fresh && !recentCheck) void revalidate(url, page, init)
        return abortable(Promise.resolve(res), signal)
      }
      forget(url)
    }
  }
  try {
    const res = read
      ? await abortable(network(url, { ...init, headers }, force), signal)
      : await globalThis.fetch(input, init)
    const metadata = notifyResponse(res, url, page)
    if (read && metadata?.state === "refreshing") void revalidate(url, page, init)
    if (method !== "GET" && method !== "HEAD" && res.ok) {
      // UI/prefs writes must not discard GitHub snapshots. Invalidate only the
      // upstream affected; the host applies the same partition on disk.
      const scope = url.includes("/chat-turns/") ? "chat-turns" : url.includes("/chat/") ? "chat" : url.includes("/forge/") ? "forge" : url.includes("/catalog/") ? "catalog" : "torque"
      forgetScope(scope)
      if (scope === "torque") { forgetScope("planner"); forgetScope("activity") }
    }
    return res
  } catch (error) {
    if (!(error instanceof Error && error.name === "AbortError")) {
      const prior = snapshots.find((s) => s.url === url && s.page === page)
      const scope = prior?.scope ?? (url.includes("/chat-turns/") ? "chat-turns" : url.includes("/chat/") ? "chat" : url.includes("/catalog/") ? "catalog" : url.includes("/activity/") ? "activity" : url.includes("/planner/") ? "planner" : url.includes("/forge/") ? "forge" : null)
      if (scope) publish({ url, page, scope, updatedAt: null, freshSeconds: 0, ...prior, state: "error", problem: "host_unreachable" })
    }
    throw error
  }
}

/** Refresh data in place: keep rows, selection and drafts while requests run. */
export async function refreshPageData(): Promise<void> {
  const page = window.location.pathname
  const urls = [...new Set(snapshots.filter((s) => s.page === page).map((s) => s.url))]
  await Promise.all(urls.map((url) => revalidate(url, page)))
}

export function useDataFreshness() {
  return useSyncExternalStore(
    (listener) => { listeners.add(listener); return () => { listeners.delete(listener) } },
    () => snapshots,
    () => empty,
  )
}

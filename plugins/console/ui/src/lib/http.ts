import { hostFetch } from "@tachyon/host-sdk"
// Shared HTTP plumbing for the console's real clients (state store, step-up,
// approvals, HITL, workflow runs, ref briefs...). Every call goes through the
// console plugin backend, which proxies /api/plugins/console/v1/* to Torque
// /api/v1/*. Missing upstream routes surface as ConsoleHttpError (404 /
// 'unavailable'), never as fake data.
import { pluginApiBase } from '@tachyon/host-sdk'

export const CONSOLE_API_BASE = `${pluginApiBase('console')}/v1`

export type ConsoleHttpErrorCode = 'not_found' | 'unauthorized' | 'forbidden' | 'conflict' | 'invalid' | 'unavailable' | 'error'

export class ConsoleHttpError extends Error {
  readonly status: number
  readonly code: ConsoleHttpErrorCode | string
  constructor(status: number, code: string, message: string) {
    super(message)
    this.name = 'ConsoleHttpError'
    this.status = status
    this.code = code
  }
}

function codeFor(status: number): ConsoleHttpErrorCode {
  if (status === 404) return 'not_found'
  if (status === 401) return 'unauthorized'
  if (status === 403) return 'forbidden'
  if (status === 409) return 'conflict'
  if (status === 400 || status === 422) return 'invalid'
  if (status === 0 || status >= 502) return 'unavailable'
  return 'error'
}

export type FetchLike = (input: string, init?: RequestInit) => Promise<Response>

export interface ConsoleRequestInit extends Omit<RequestInit, 'body'> {
  body?: unknown
  fetch?: FetchLike
}

/** JSON request against the console plugin API. `path` is relative to CONSOLE_API_BASE (leading slash). */
export async function consoleJson<T>(path: string, init: ConsoleRequestInit = {}): Promise<T> {
  const { body, fetch: f, headers, ...rest } = init
  const doFetch: FetchLike = f ?? hostFetch
  let res: Response
  try {
    res = await doFetch(`${CONSOLE_API_BASE}${path}`, {
      ...rest,
      headers: { Accept: 'application/json', ...(body !== undefined ? { 'Content-Type': 'application/json' } : {}), ...(headers as Record<string, string> | undefined) },
      ...(body !== undefined ? { body: JSON.stringify(body) } : {}),
    })
  } catch (err) {
    if (err instanceof DOMException && err.name === 'AbortError') throw err
    throw new ConsoleHttpError(0, 'unavailable', err instanceof Error ? err.message : 'Network error')
  }
  const text = await res.text()
  const isJson = (res.headers.get('content-type') ?? '').includes('json')
  let parsed: unknown = undefined
  if (text && isJson) {
    try {
      parsed = JSON.parse(text)
    } catch {
      parsed = undefined
    }
  }
  if (!res.ok) {
    const e = (parsed as { error?: unknown } | undefined)?.error
    const code = typeof e === 'object' && e && 'code' in e ? String((e as { code: unknown }).code) : codeFor(res.status)
    const message =
      typeof e === 'string' ? e : typeof e === 'object' && e && 'message' in e ? String((e as { message: unknown }).message) : `${res.status} ${res.statusText || 'request failed'}`.trim()
    throw new ConsoleHttpError(res.status, code, message)
  }
  if (text && !isJson) throw new ConsoleHttpError(res.status, 'unavailable', 'Endpoint not available (non-JSON response)')
  return parsed as T
}

/** Accepts `{data: T}` envelopes and bare bodies. */
export function unwrap<T>(v: unknown): T {
  return (v && typeof v === 'object' && 'data' in (v as object) ? (v as { data: T }).data : v) as T
}

/** Accepts `T[]`, `{items: T[]}` and `{data: T[] | {items}}`. */
export function unwrapList<T>(v: unknown, key = 'items'): T[] {
  const d = unwrap<unknown>(v)
  if (Array.isArray(d)) return d as T[]
  if (d && typeof d === 'object' && Array.isArray((d as Record<string, unknown>)[key])) return (d as Record<string, T[]>)[key]
  return []
}

export function newIdempotencyKey(): string {
  return typeof crypto !== 'undefined' && 'randomUUID' in crypto ? crypto.randomUUID() : `k-${Date.now()}-${Math.random().toString(36).slice(2)}`
}

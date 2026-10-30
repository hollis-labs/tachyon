import { hostFetch } from '@tachyon/host-sdk'
import { CONSOLE_API_BASE, type FetchLike } from '../../lib/http'
import type { ServerAlert, ServerAlertList, ServerCounts, ServerOp, ServerPrefs, ServerSource } from './types'
import type { ConsolePrefs } from '../../prefs/types'

export const ALERTS_BASE = '/alerts'

/** The alerts API answers errors as {code, message}; a version_conflict also carries the server's `current` copy. */
export class AlertsApiError extends Error {
  constructor(
    readonly status: number,
    readonly code: string,
    message: string,
    readonly current?: ServerAlert,
  ) {
    super(message)
    this.name = 'AlertsApiError'
  }
}
export const isVersionConflict = (e: unknown): e is AlertsApiError => e instanceof AlertsApiError && e.code === 'version_conflict'


export interface AlertsClient {
  list(view: 'active' | 'history', opts?: { limit?: number; cursor?: string }): Promise<ServerAlertList>
  counts(): Promise<ServerCounts>
  op(id: string, op: ServerOp, expectedVersion: number): Promise<ServerAlert>
  bulk(op: 'read' | 'dismiss', scope: { ids: string[] } | { scope: 'all_active' }, expectedSeq?: number): Promise<{ updated: string[]; skipped: string[]; counts: ServerCounts; seq: number }>
  prefs(): Promise<ServerPrefs>
  savePrefs(p: ServerPrefs): Promise<ServerPrefs>
  migratePrefs(legacy: Partial<ConsolePrefs>): Promise<{ prefs: ServerPrefs; applied: boolean }>
  sources(): Promise<{ sources: ServerSource[]; server_now_ms: number }>
  /** Stream URL resuming after `seq`; EventSource also resends Last-Event-ID itself on reconnect. */
  streamUrl(after?: number): string
}

export function createAlertsClient(fetcher: FetchLike = hostFetch): AlertsClient {
  async function json<T>(path: string, init: { method?: string; body?: unknown } = {}): Promise<T> {
    let res: Response
    try {
      res = await fetcher(`${CONSOLE_API_BASE}${ALERTS_BASE}${path}`, {
        method: init.method,
        headers: { Accept: 'application/json', ...(init.body !== undefined ? { 'Content-Type': 'application/json' } : {}) },
        ...(init.body !== undefined ? { body: JSON.stringify(init.body) } : {}),
      })
    } catch (e) {
      throw new AlertsApiError(0, 'unavailable', e instanceof Error ? e.message : 'Network error')
    }
    const body = (await res.json().catch(() => undefined)) as (Partial<{ code: string; message: string; current: ServerAlert }> & T) | undefined
    if (!res.ok) throw new AlertsApiError(res.status, body?.code ?? 'unavailable', body?.message ?? 'The alerts service is unavailable.', body?.current)
    if (body === undefined) throw new AlertsApiError(res.status, 'unavailable', 'The alerts service sent an unreadable answer.')
    return body as T
  }
  return {
    list(view, { limit = 100, cursor } = {}) {
      const q = new URLSearchParams({ view, limit: String(limit), ...(cursor ? { cursor } : {}) })
      return json<ServerAlertList>(`?${q}`)
    },
    counts: () => json<ServerCounts>('/counts'),
    op: (id, op, expectedVersion) => json<ServerAlert>(`/${encodeURIComponent(id)}/ops`, { method: 'POST', body: { ...op, expected_version: expectedVersion } }),
    bulk: (op, scope, expectedSeq) => json('/ops/bulk', { method: 'POST', body: { op, ...scope, ...(expectedSeq ? { expected_seq: expectedSeq } : {}) } }),
    prefs: () => json<ServerPrefs>('/prefs'),
    savePrefs: (p) => json<ServerPrefs>('/prefs', { method: 'PUT', body: p }),
    migratePrefs: (legacy) => json('/prefs/migrate', { method: 'POST', body: legacy }),
    sources: () => json('/sources'),
    streamUrl: (after) => `${CONSOLE_API_BASE}${ALERTS_BASE}/stream${after ? `?after=${after}` : ''}`,
  }
}

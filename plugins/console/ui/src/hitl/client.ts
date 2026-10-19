// HitlClient: the typed seam between the HITL cards and the human queue.
// HttpHitlClient below is the live implementation (Torque facade over
// Tangent's queue, tangent_hitl_*/interaction_*); tests use testkit/hitl.ts.
// Needs are recorded in BACKEND_NEEDS.md.
import type { ConfirmResult } from '../seams/identity-confirm'
import { ConsoleHttpError, consoleJson, newIdempotencyKey, unwrap, unwrapList, type FetchLike } from '../lib/http'
import { createListPoller } from '../lib/poller'
import { parseHitlRequest } from './model'
import type { HitlFormValues, HitlRequest, HitlResponse, HitlStatus } from './types'

export interface HitlSubmitContext {
  /** Present when the ActionBus ran IdentityConfirm. Forward `token` to the backend; never log it. */
  confirmation?: ConfirmResult
}

export interface HitlListOptions {
  status?: HitlStatus[]
}

export interface HitlClient {
  list(opts?: HitlListOptions): Promise<HitlRequest[]>
  get(id: string): Promise<HitlRequest | undefined>
  respond(id: string, response: HitlResponse, ctx: HitlSubmitContext): Promise<HitlRequest>
  /** The secret value is passed here and nowhere else. Implementations must not log, persist or echo it. */
  submitSecret(id: string, value: string, ctx: HitlSubmitContext): Promise<HitlRequest>
  /** Server push (or local echo) of changed requests. */
  subscribe(listener: (changed: HitlRequest[]) => void): () => void
}

/** Naming in the task text: the same seam. */
export type HitlSource = HitlClient

export class HitlError extends Error {
  code: 'not_found' | 'not_pending' | 'expired' | 'confirm_required' | 'invalid'
  constructor(code: HitlError['code'], message: string) {
    super(message)
    this.code = code
  }
}

/**
 * One-shot hand-off for secret values. The card puts the typed value here and
 * puts only the opaque handle in ActionBus params, so the value never appears in
 * action records, controller state, the StateStore or logs. `take` consumes it.
 */
export class SecretHandoff {
  private slots = new Map<string, string>()
  private n = 0
  put(value: string): string {
    const h = `sh-${++this.n}-${Math.random().toString(36).slice(2, 8)}`
    this.slots.set(h, value)
    return h
  }
  take(handle: string): string | undefined {
    const v = this.slots.get(handle)
    this.slots.delete(handle)
    return v
  }
  discard(handle: string): void {
    this.slots.delete(handle)
  }
  get size(): number {
    return this.slots.size
  }
}

export const secretHandoff = new SecretHandoff()

export interface HttpHitlClientOptions {
  fetch?: FetchLike
  pollMs?: number
}

/**
 * Live HitlClient (C6) over the console plugin API (Torque facade over the
 * Tangent human queue + go-hitl):
 *   GET  /hitl/requests?status=pending[,..]   -> HitlRequest[] (bare | {items} | {data})
 *   GET  /hitl/requests/{id}                  -> HitlRequest (404 -> undefined)
 *   POST /hitl/requests/{id}/respond          {response, confirmationToken, idempotencyKey, planHash?}
 *   POST /hitl/requests/{id}/secret           {value, confirmationToken, idempotencyKey}
 * Push: polls the pending list until a relayed `hitl` stream exists.
 */
export class HttpHitlClient implements HitlClient {
  private readonly opts: HttpHitlClientOptions
  private readonly poller
  constructor(opts: HttpHitlClientOptions = {}) {
    this.opts = opts
    this.poller = createListPoller<HitlRequest>({ load: () => this.list({ status: ['pending'] }), key: (r) => r.id, intervalMs: opts.pollMs })
  }

  async list(opts: HitlListOptions = {}): Promise<HitlRequest[]> {
    const qs = opts.status?.length ? `?status=${encodeURIComponent(opts.status.join(','))}` : ''
    const rows = unwrapList<unknown>(await consoleJson('/hitl/requests' + qs, { fetch: this.opts.fetch }))
    return rows.map((r) => parseHitlRequest(r)).filter((r): r is HitlRequest => !!r)
  }

  async get(id: string): Promise<HitlRequest | undefined> {
    try {
      return parseHitlRequest(unwrap(await consoleJson(`/hitl/requests/${encodeURIComponent(id)}`, { fetch: this.opts.fetch })))
    } catch (err) {
      if (err instanceof ConsoleHttpError && err.status === 404) return undefined
      throw err
    }
  }

  private async write(id: string, path: string, body: Record<string, unknown>): Promise<HitlRequest> {
    try {
      const out = parseHitlRequest(unwrap(await consoleJson(`/hitl/requests/${encodeURIComponent(id)}/${path}`, { method: 'POST', fetch: this.opts.fetch, body: { ...body, idempotencyKey: newIdempotencyKey() } })))
      if (!out) throw new HitlError('invalid', 'Malformed response from the HITL queue')
      this.poller.emit([out])
      return out
    } catch (err) {
      if (err instanceof ConsoleHttpError) {
        const code = err.status === 404 ? 'not_found' : err.code === 'expired' ? 'expired' : err.code === 'not_pending' || err.status === 409 ? 'not_pending' : err.status === 401 || err.status === 403 ? 'confirm_required' : 'invalid'
        throw new HitlError(code, err.message)
      }
      throw err
    }
  }

  respond(id: string, response: HitlResponse, ctx: HitlSubmitContext): Promise<HitlRequest> {
    return this.write(id, 'respond', { response, confirmationToken: ctx.confirmation?.token })
  }

  submitSecret(id: string, value: string, ctx: HitlSubmitContext): Promise<HitlRequest> {
    return this.write(id, 'secret', { value, confirmationToken: ctx.confirmation?.token })
  }

  subscribe(listener: (changed: HitlRequest[]) => void): () => void {
    return this.poller.subscribe(listener)
  }
}
export function hasValue(v: HitlFormValues[string] | undefined, kind: string): boolean {
  if (kind === 'checkbox') return v === true
  if (typeof v === 'number') return Number.isFinite(v)
  return typeof v === 'string' && v.trim() !== ''
}

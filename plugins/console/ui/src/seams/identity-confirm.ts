// IdentityConfirm: the step-up seam in front of every approve/apply that
// authorises a change. The real implementation will call the auth workstream's
// API (undefined at the time of writing); the request/result shape below is the
// stable part: who, what, scope, expiry, result.

import type { ConsoleRef } from '../events/types'
import { consoleJson, unwrap, type FetchLike } from '../lib/http'

export interface ConfirmWho {
  id: string
  label?: string
}

export interface ConfirmWhat {
  /** Machine-readable action, e.g. "agent.apply" or "approval.approve". */
  action: string
  /** Human-readable one-liner shown in the confirm prompt. */
  title: string
  summary?: string
  subject?: ConsoleRef
}

export interface ConfirmRequest {
  /** Defaults to the signed-in user when omitted. */
  who?: ConfirmWho
  what: ConfirmWhat
  /** Permission scopes the grant must cover. */
  scope: string[]
  /** How long a grant stays valid; implementations may shorten it. */
  ttlMs?: number
}

export type ConfirmOutcome = 'granted' | 'denied' | 'cancelled' | 'expired' | 'unavailable'

export interface ConfirmResult {
  outcome: ConfirmOutcome
  who: ConfirmWho
  what: ConfirmWhat
  scope: string[]
  /** Epoch ms when the grant lapses (granted only). */
  expiresAt: number | null
  issuedAt: number
  /** Opaque proof the backend verifies when the action executes. Never log it. */
  token?: string
  reason?: string
}

export interface IdentityConfirm {
  confirm(req: ConfirmRequest): Promise<ConfirmResult>
}

export function isGranted(r: ConfirmResult, now = Date.now()): boolean {
  return r.outcome === 'granted' && (r.expiresAt === null || r.expiresAt > now)
}

export type MockConfirmMode = 'auto-grant' | 'auto-deny' | 'manual'

export interface PendingConfirm {
  id: string
  request: ConfirmRequest
}

export interface MockIdentityConfirmOptions {
  mode?: MockConfirmMode
  who?: ConfirmWho
  now?: () => number
  defaultTtlMs?: number
}

/**
 * Test/dev step-up (never the runtime default; see HttpIdentityConfirm).
 * 'auto-grant'/'auto-deny' resolve immediately (tests only);
 * 'manual' queues the request so a dialog component can render `pending()` and
 * call `resolve(id, outcome)`; the UI is a later task's concern, this is its hook.
 */
export class MockIdentityConfirm implements IdentityConfirm {
  mode: MockConfirmMode
  private readonly who: ConfirmWho
  private readonly now: () => number
  private readonly ttl: number
  private seq = 0
  private queue = new Map<string, { req: ConfirmRequest; done: (r: ConfirmResult) => void }>()
  private listeners = new Set<() => void>()
  private snapshot: PendingConfirm[] = []

  constructor(opts: MockIdentityConfirmOptions = {}) {
    this.mode = opts.mode ?? 'auto-grant'
    this.who = opts.who ?? { id: 'mock-user', label: 'Mock User' }
    this.now = opts.now ?? Date.now
    this.ttl = opts.defaultTtlMs ?? 60_000
  }

  private result(req: ConfirmRequest, outcome: ConfirmOutcome, reason?: string): ConfirmResult {
    const issuedAt = this.now()
    const granted = outcome === 'granted'
    return {
      outcome,
      who: req.who ?? this.who,
      what: req.what,
      scope: req.scope,
      issuedAt,
      expiresAt: granted ? issuedAt + (req.ttlMs ?? this.ttl) : null,
      ...(granted ? { token: `mock-${issuedAt}-${++this.seq}` } : {}),
      ...(reason ? { reason } : {}),
    }
  }

  confirm(req: ConfirmRequest): Promise<ConfirmResult> {
    if (this.mode === 'auto-grant') return Promise.resolve(this.result(req, 'granted'))
    if (this.mode === 'auto-deny') return Promise.resolve(this.result(req, 'denied', 'mock policy'))
    return new Promise((done) => {
      this.queue.set(`c${++this.seq}`, { req, done })
      this.publish()
    })
  }

  pending(): PendingConfirm[] {
    return this.snapshot
  }

  resolve(id: string, outcome: ConfirmOutcome, reason?: string): void {
    const q = this.queue.get(id)
    if (!q) return
    this.queue.delete(id)
    q.done(this.result(q.req, outcome, reason))
    this.publish()
  }

  /** useSyncExternalStore-compatible. */
  subscribe = (l: () => void): (() => void) => {
    this.listeners.add(l)
    return () => this.listeners.delete(l)
  }

  private publish() {
    this.snapshot = [...this.queue].map(([id, q]) => ({ id, request: q.req }))
    this.listeners.forEach((l) => l())
  }
}

export interface HttpIdentityConfirmOptions {
  fetch?: FetchLike
  now?: () => number
  defaultTtlMs?: number
}

interface StepUpResponse {
  outcome?: ConfirmOutcome
  token?: string
  expiresAt?: number | string | null
  issuedAt?: number | string
  who?: ConfirmWho
  reason?: string
}

const ms = (v: number | string | null | undefined): number | null =>
  v === null || v === undefined ? null : typeof v === 'number' ? v : Number.isNaN(Date.parse(v)) ? null : Date.parse(v)

/**
 * Real step-up (C0.3): `POST /auth/step-up {what{action,subject}, scope[], ttl}`
 * returns `{outcome, token, expiresAt, issuedAt}`. The issuer owns the
 * confirmation UX (passkey/WebAuthn), so this brings no dialog of its own and
 * `StepUpHost` renders nothing for it. When the endpoint is missing or fails
 * the outcome is `unavailable` with the reason: never a silent grant.
 */
export class HttpIdentityConfirm implements IdentityConfirm {
  private readonly opts: HttpIdentityConfirmOptions
  constructor(opts: HttpIdentityConfirmOptions = {}) {
    this.opts = opts
  }

  async confirm(req: ConfirmRequest): Promise<ConfirmResult> {
    const now = this.opts.now ?? Date.now
    const ttl = req.ttlMs ?? this.opts.defaultTtlMs ?? 60_000
    const base = { who: req.who ?? { id: 'operator' }, what: req.what, scope: req.scope }
    try {
      const raw = unwrap<StepUpResponse>(
        await consoleJson('/auth/step-up', {
          method: 'POST',
          fetch: this.opts.fetch,
          body: { who: req.who, what: { action: req.what.action, title: req.what.title, summary: req.what.summary, subject: req.what.subject }, scope: req.scope, ttl },
        }),
      )
      const outcome = raw?.outcome ?? (raw?.token ? 'granted' : 'denied')
      const issuedAt = ms(raw?.issuedAt) ?? now()
      return {
        ...base,
        ...(raw?.who ? { who: raw.who } : {}),
        outcome,
        issuedAt,
        expiresAt: outcome === 'granted' ? (ms(raw?.expiresAt) ?? issuedAt + ttl) : null,
        ...(outcome === 'granted' && raw?.token ? { token: raw.token } : {}),
        ...(raw?.reason ? { reason: raw.reason } : {}),
      }
    } catch (err) {
      return { ...base, outcome: 'unavailable', issuedAt: now(), expiresAt: null, reason: `Identity confirmation unavailable: ${err instanceof Error ? err.message : String(err)}` }
    }
  }
}

// TEST-ONLY in-memory HitlClient (ported from Torque hitl/client.ts). Never imported at runtime.
import { isGranted } from '../seams/identity-confirm'
import { HitlError, hasValue, type HitlClient, type HitlListOptions, type HitlSubmitContext } from '../hitl/client'
import type { HitlAuditEntry, HitlOutcome, HitlRequest, HitlResponse } from '../hitl/types'

export interface MockHitlClientOptions {
  now?: () => number
  latencyMs?: number
  /** Request ids whose next submit fails (for error-state tests). */
  failIds?: string[]
}

export class MockHitlClient implements HitlClient {
  private items = new Map<string, HitlRequest>()
  private listeners = new Set<(c: HitlRequest[]) => void>()
  private readonly now: () => number
  private readonly latency: number
  failIds: Set<string>
  /** Count only: the mock never keeps secret values. */
  secretsReceived = 0

  constructor(initial: HitlRequest[] = [], opts: MockHitlClientOptions = {}) {
    this.now = opts.now ?? Date.now
    this.latency = opts.latencyMs ?? 0
    this.failIds = new Set(opts.failIds)
    initial.forEach((r) => this.items.set(r.id, structuredClone(r)))
  }

  private wait() {
    return this.latency ? new Promise((r) => setTimeout(r, this.latency)) : Promise.resolve()
  }
  private emit(r: HitlRequest) {
    this.listeners.forEach((l) => l([structuredClone(r)]))
  }
  /** Seed or replace a request, as if the queue produced it. */
  push(r: HitlRequest) {
    this.items.set(r.id, structuredClone(r))
    this.emit(r)
  }

  async list(opts: HitlListOptions = {}) {
    await this.wait()
    return [...this.items.values()].filter((r) => !opts.status || opts.status.includes(r.status)).map((r) => structuredClone(r))
  }
  async get(id: string) {
    const r = this.items.get(id)
    return r ? structuredClone(r) : undefined
  }
  subscribe(l: (c: HitlRequest[]) => void) {
    this.listeners.add(l)
    return () => this.listeners.delete(l)
  }

  private open(id: string): HitlRequest {
    const r = this.items.get(id)
    if (!r) throw new HitlError('not_found', `Unknown request ${id}`)
    if (r.status !== 'pending') throw new HitlError('not_pending', `Request is already ${r.status}`)
    if (r.expiresAt !== undefined && r.expiresAt <= this.now()) throw new HitlError('expired', 'Request has expired')
    if (this.failIds.has(id)) {
      this.failIds.delete(id)
      throw new HitlError('invalid', 'Mock queue unavailable')
    }
    return r
  }

  private close(r: HitlRequest, entry: Omit<HitlAuditEntry, 'at'>, outcome: Omit<HitlOutcome, 'at'>, ctx: HitlSubmitContext) {
    const at = this.now()
    const audit: HitlAuditEntry[] = [...r.audit]
    if (ctx.confirmation) {
      audit.push({ at, actor: ctx.confirmation.who.id, action: 'confirmed', note: `identity step-up: ${ctx.confirmation.scope.join(', ') || 'no scope'}` })
    }
    audit.push({ at, ...entry })
    const next = { ...r, status: 'answered' as const, audit, outcome: { ...outcome, at } } as HitlRequest
    this.items.set(r.id, next)
    this.emit(next)
    return structuredClone(next)
  }

  async respond(id: string, res: HitlResponse, ctx: HitlSubmitContext) {
    await this.wait()
    const r = this.open(id)
    const actor = ctx.confirmation?.who.id ?? 'operator'
    if (res.type !== r.type) throw new HitlError('invalid', `Response type ${res.type} does not match ${r.type}`)
    if (r.type === 'approval' && res.type === 'approval') {
      if (res.decision === 'approve' && !(ctx.confirmation && isGranted(ctx.confirmation, this.now()))) {
        throw new HitlError('confirm_required', 'Approval requires identity confirmation')
      }
      if (res.decision === 'defer' && !r.allowDefer) throw new HitlError('invalid', 'Deferral is not allowed for this request')
      const map = { approve: ['approved', 'Approved'], reject: ['rejected', 'Rejected'], defer: ['deferred', 'Deferred'] } as const
      const [action, label] = map[res.decision]
      const out = this.close(r, { actor, action, note: res.reason || undefined }, { label, by: actor }, ctx)
      if (res.decision === 'defer') {
        // A deferral keeps the request open for later.
        const reopened = { ...out, status: 'pending' as const, outcome: undefined } as HitlRequest
        this.items.set(id, reopened)
        this.emit(reopened)
        return structuredClone(reopened)
      }
      return out
    }
    if (r.type === 'question' && res.type === 'question') {
      const picks = Array.isArray(res.answer) ? res.answer : [res.answer]
      if (picks.length === 0 || picks.every((p) => !p.trim())) throw new HitlError('invalid', 'An answer is required')
      if (r.mode !== 'text' && !r.allowOther) {
        const known = new Set(r.options.map((o) => o.value))
        if (picks.some((p) => !known.has(p))) throw new HitlError('invalid', 'Answer is not one of the options')
      }
      return this.close(r, { actor, action: 'answered', note: picks.join(', ').slice(0, 200) }, { label: 'Answered', by: actor }, ctx)
    }
    if (r.type === 'form' && res.type === 'form') {
      const missing = r.fields.filter((f) => f.required && !hasValue(res.values[f.id], f.kind))
      if (missing.length) throw new HitlError('invalid', `Required: ${missing.map((m) => m.label).join(', ')}`)
      return this.close(r, { actor, action: 'submitted', note: `${Object.keys(res.values).length} fields` }, { label: 'Submitted', by: actor }, ctx)
    }
    throw new HitlError('invalid', 'Unsupported response')
  }

  async submitSecret(id: string, value: string, ctx: HitlSubmitContext) {
    await this.wait()
    const r = this.open(id)
    if (r.type !== 'secret') throw new HitlError('invalid', 'Not a secret request')
    if (!value) throw new HitlError('invalid', 'A value is required')
    this.secretsReceived += 1 // the value itself is dropped here
    return this.close(r, { actor: ctx.confirmation?.who.id ?? 'operator', action: 'submitted', note: 'secret delivered (value not retained)' }, { label: 'Delivered', by: ctx.confirmation?.who.id }, ctx)
  }
}


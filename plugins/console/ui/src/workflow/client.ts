// WorkflowClient: the typed seam between workflow cards and the orchestration
// backend (Torque checkpoints, Hadron gates/signals, agent steering via M1).
// HttpWorkflowClient is the live implementation (tests: testkit/workflow.ts);
// needs are in BACKEND_NEEDS.md.
import type { ConsoleEvent } from '../events/types'
import type { ConfirmResult } from '../seams/identity-confirm'
import { ConsoleHttpError, consoleJson, newIdempotencyKey, unwrap, unwrapList, type FetchLike } from '../lib/http'
import { createListPoller } from '../lib/poller'
import type { GateDecision, PlanDecision, SteerCommand, WorkflowRun } from './types'

export interface WorkflowCtx {
  /** Present when the ActionBus ran IdentityConfirm. Forward `token` to the backend; never log it. */
  confirmation?: ConfirmResult
}

export interface WorkflowClient {
  list(): Promise<WorkflowRun[]>
  get(id: string): Promise<WorkflowRun | undefined>
  steer(runId: string, cmd: SteerCommand, ctx: WorkflowCtx): Promise<WorkflowRun>
  /** hadron_workflow_gate_submit. */
  submitGate(runId: string, gateId: string, decision: GateDecision, note: string | undefined, ctx: WorkflowCtx): Promise<WorkflowRun>
  /** torque_task_checkpoint_respond. */
  respondCheckpoint(runId: string, checkpointId: string, answer: string, ctx: WorkflowCtx): Promise<WorkflowRun>
  /** `included` is the edited subset of plan item ids; approving with fewer than proposed is an edit. */
  decidePlan(runId: string, decision: PlanDecision, included: string[], note: string | undefined, ctx: WorkflowCtx): Promise<WorkflowRun>
  subscribe(listener: (changed: WorkflowRun[]) => void): () => void
  /** Optional: a mock/local client folds Console events itself; a live client gets projections from the server. */
  ingest?(event: ConsoleEvent): void
}

export class WorkflowError extends Error {
  code: 'not_found' | 'not_active' | 'confirm_required' | 'invalid' | 'conflict'
  constructor(code: WorkflowError['code'], message: string) {
    super(message)
    this.code = code
  }
}

export interface HttpWorkflowClientOptions {
  fetch?: FetchLike
  pollMs?: number
}

const ts = (v: unknown): number => (typeof v === 'number' ? v : typeof v === 'string' && !Number.isNaN(Date.parse(v)) ? Date.parse(v) : 0)

function parseRun(raw: unknown): WorkflowRun | undefined {
  if (!raw || typeof raw !== 'object' || typeof (raw as { id?: unknown }).id !== 'string') return undefined
  const r = raw as WorkflowRun & { createdAt: unknown; updatedAt: unknown }
  return { ...r, phases: r.phases ?? [], subjects: r.subjects ?? [], audit: r.audit ?? [], createdAt: ts(r.createdAt), updatedAt: ts(r.updatedAt) }
}

/**
 * Live WorkflowClient (C9) over the console plugin API (Torque facade over
 * Hadron runs + Torque checkpoints):
 *   GET  /workflow-runs                          -> WorkflowRun[] (bare | {items} | {data})
 *   GET  /workflow-runs/{id}                     -> WorkflowRun (404 -> undefined)
 *   POST /workflow-runs/{id}/steer               {cmd, confirmationToken, idempotencyKey}
 *   POST /workflow-runs/{id}/gates/{gateId}      {decision, note?, confirmationToken, idempotencyKey}
 *   POST /workflow-runs/{id}/plan                {decision, included[], note?, confirmationToken, idempotencyKey}
 *   POST /checkpoints/{cid}/respond              {response: answer} then re-reads the run
 * Server-side projection: no `ingest`. Push: polls the run list until a relayed stream exists.
 */
export class HttpWorkflowClient implements WorkflowClient {
  private readonly opts: HttpWorkflowClientOptions
  private readonly poller
  constructor(opts: HttpWorkflowClientOptions = {}) {
    this.opts = opts
    this.poller = createListPoller<WorkflowRun>({ load: () => this.list(), key: (r) => r.id, intervalMs: opts.pollMs })
  }

  async list(): Promise<WorkflowRun[]> {
    return unwrapList<unknown>(await consoleJson('/workflow-runs', { fetch: this.opts.fetch })).map(parseRun).filter((r): r is WorkflowRun => !!r)
  }

  async get(id: string): Promise<WorkflowRun | undefined> {
    try {
      return parseRun(unwrap(await consoleJson(`/workflow-runs/${encodeURIComponent(id)}`, { fetch: this.opts.fetch })))
    } catch (err) {
      if (err instanceof ConsoleHttpError && err.status === 404) return undefined
      throw err
    }
  }

  private async post(path: string, body: Record<string, unknown>, ctx: WorkflowCtx, rereadId?: string): Promise<WorkflowRun> {
    try {
      const res = unwrap(await consoleJson(path, { method: 'POST', fetch: this.opts.fetch, body: { ...body, confirmationToken: ctx.confirmation?.token, idempotencyKey: newIdempotencyKey() } }))
      const run = rereadId ? await this.get(rereadId) : parseRun(res)
      if (!run) throw new WorkflowError('invalid', 'Malformed response from the workflow service')
      this.poller.emit([run])
      return run
    } catch (err) {
      if (err instanceof ConsoleHttpError) {
        const code = err.status === 404 ? 'not_found' : err.status === 409 ? 'conflict' : err.status === 401 || err.status === 403 ? 'confirm_required' : err.code === 'not_active' ? 'not_active' : 'invalid'
        throw new WorkflowError(code, err.message)
      }
      throw err
    }
  }

  steer(runId: string, cmd: SteerCommand, ctx: WorkflowCtx) {
    return this.post(`/workflow-runs/${encodeURIComponent(runId)}/steer`, { cmd }, ctx)
  }

  submitGate(runId: string, gateId: string, decision: GateDecision, note: string | undefined, ctx: WorkflowCtx) {
    return this.post(`/workflow-runs/${encodeURIComponent(runId)}/gates/${encodeURIComponent(gateId)}`, { decision, note }, ctx)
  }

  respondCheckpoint(runId: string, checkpointId: string, answer: string, ctx: WorkflowCtx) {
    return this.post(`/checkpoints/${encodeURIComponent(checkpointId)}/respond`, { response: answer }, ctx, runId)
  }

  decidePlan(runId: string, decision: PlanDecision, included: string[], note: string | undefined, ctx: WorkflowCtx) {
    return this.post(`/workflow-runs/${encodeURIComponent(runId)}/plan`, { decision, included, note }, ctx)
  }

  subscribe(listener: (changed: WorkflowRun[]) => void): () => void {
    return this.poller.subscribe(listener)
  }
}

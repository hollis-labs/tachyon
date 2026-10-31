import { hostFetch } from "@tachyon/host-sdk"
// Typed HTTP implementation of ForgeClient against the PROPOSED Torque forge
// REST surface specified in BACKEND_REQUIREMENTS.md. The browser talks only to
// the Torque API; Torque fronts the Cerberus forge plugin. No tokens here.
//
// Wire format: JSON, camelCase, identical to ./types.ts. Responses are wrapped
// as `{ "data": ... }` (a bare body is also accepted). Unknown fields are
// passed through untouched; missing collection fields are defaulted.

import type { ForgeClient } from './client'
import { newIdempotencyKey } from '../lib/http'
import {
  ForgeError,
  type ForgeUser,
  type MergeInput,
  type MergePreflight,
  type MergeMethod,
  type MergeResult,
  type AddCommentInput,
  type AssignInput,
  type Commit,
  type FileDiff,
  type ForgeComment,
  type ForgeErrorCode,
  type ListPrsQuery,
  type Page,
  type PendingPrSummary,
  type PrRef,
  type PullRequest,
  type Repo,
  type RepoRef,
  type Review,
  type ReviewDraft,
  type ReviewValidation,
  type CommentCheck,
  type ReviewInput,
} from './types'

export const DEFAULT_FORGE_BASE = '/api/plugins/console/v1/forge'

export type FetchLike = (input: string, init?: RequestInit) => Promise<Response>

export interface HttpForgeClientOptions {
  /** Base path or absolute URL; default `/api/plugins/console/v1/forge`. Trailing slash ignored. */
  baseUrl?: string
  /** Injectable for tests; defaults to global fetch. */
  fetch?: FetchLike
  /**
   * Supplies the IdentityConfirm proof token for write ops (sent as
   * `X-Identity-Confirm`). Resolve it after running IdentityConfirm; return
   * undefined when none is available (the backend will answer `unauthorized`).
   */
  confirmToken?: () => string | undefined | Promise<string | undefined>
  /** Extra headers (e.g. auth in non-same-origin setups). */
  headers?: Record<string, string>
  /** Idempotency key generator for writes. */
  idempotencyKey?: () => string
}

/** Map Cerberus plugin codes and HTTP statuses onto ForgeErrorCode. */
export function mapForgeErrorCode(code: string | undefined, status: number): ForgeErrorCode {
  switch (code) {
    case 'not_found':
    case 'invalid_args':
    case 'unauthorized':
    case 'unavailable':
    case 'conflict':
    case 'rate_limited':
      return code
    case 'credential_missing':
    case 'identity_required':
    case 'forbidden':
      return 'unauthorized'
    case 'connector_unavailable':
    case 'operation_failed':
      return 'unavailable'
  }
  if (code) return code // unknown backend codes pass through (open union)
  if (status === 400 || status === 422) return 'invalid_args'
  if (status === 401 || status === 403) return 'unauthorized'
  if (status === 404) return 'not_found'
  if (status === 409) return 'conflict'
  if (status === 429) return 'rate_limited'
  return 'unavailable'
}

const enc = encodeURIComponent

function repoPath(r: RepoRef): string {
  return `/repos/${enc(r.host)}/${enc(r.owner)}/${enc(r.name)}`
}

function prPath(p: PrRef): string {
  return `${repoPath(p.repo)}/pulls/${p.number}`
}

function isObj(v: unknown): v is Record<string, unknown> {
  return typeof v === 'object' && v !== null && !Array.isArray(v)
}

function unwrap(body: unknown): unknown {
  return isObj(body) && 'data' in body ? body.data : body
}

function listOf<T>(v: unknown): T[] {
  if (Array.isArray(v)) return v as T[]
  if (isObj(v) && Array.isArray(v.items)) return v.items as T[]
  return []
}

function normalizePr(v: unknown): PullRequest {
  const p = (isObj(v) ? v : {}) as unknown as PullRequest
  return { ...p, assignees: p.assignees ?? [], checks: p.checks ?? [], reviews: p.reviews ?? [], mergeable: p.mergeable ?? 'unknown' }
}

function normalizePending(v: unknown): PendingPrSummary {
  const p = (isObj(v) ? v : {}) as unknown as PendingPrSummary & { checks?: { passed?: number; failed?: number; pending?: number } }
  // Wire rows carry `checks` as a rollup and assignees as {kind,id,label}; the inbox reads
  // `checksSummary` and a `login` on every assignee.
  const assignees = (p.assignees ?? []).map((a) => {
    const w = a as ForgeUser & { id?: string; label?: string }
    return { ...w, login: w.login ?? w.label ?? w.id ?? '' }
  })
  const c = p.checks
  const checksSummary = p.checksSummary ?? (c ? { passed: c.passed ?? 0, failed: c.failed ?? 0, pending: c.pending ?? 0 } : undefined)
  return { ...p, assignees, checksSummary, reasons: p.reasons ?? [], mergeable: p.mergeable ?? 'unknown' }
}

function normalizeFile(v: unknown): FileDiff {
  const f = (isObj(v) ? v : {}) as unknown as FileDiff
  return { ...f, hunks: f.hunks ?? [], additions: f.additions ?? 0, deletions: f.deletions ?? 0 }
}

export class HttpForgeClient implements ForgeClient {
  private readonly base: string
  private readonly doFetch: FetchLike
  private readonly opts: HttpForgeClientOptions

  constructor(opts: HttpForgeClientOptions = {}) {
    this.opts = opts
    this.base = (opts.baseUrl ?? DEFAULT_FORGE_BASE).replace(/\/+$/, '')
    this.doFetch = opts.fetch ?? hostFetch
  }

  private async request(method: 'GET' | 'POST' | 'PUT' | 'DELETE', path: string, body?: unknown, write = false): Promise<unknown> {
    const headers: Record<string, string> = { Accept: 'application/json', ...this.opts.headers }
    if (body !== undefined) headers['Content-Type'] = 'application/json'
    if (write) {
      const tok = await this.opts.confirmToken?.()
      if (tok) headers['X-Identity-Confirm'] = tok
      const key = (this.opts.idempotencyKey ?? newIdempotencyKey)()
      if (key) headers['Idempotency-Key'] = key
    }
    let res: Response
    try {
      res = await this.doFetch(this.base + path, { method, headers, body: body === undefined ? undefined : JSON.stringify(body) })
    } catch (e) {
      throw new ForgeError('unavailable', e instanceof Error ? e.message : 'network error')
    }
    let parsed: unknown
    const text = await res.text().catch(() => '')
    if (text) {
      try {
        parsed = JSON.parse(text)
      } catch {
        if (res.ok) throw new ForgeError('unavailable', 'invalid JSON from forge API')
      }
    }
    if (!res.ok) {
      const e = isObj(parsed) && isObj(parsed.error) ? parsed.error : isObj(parsed) ? parsed : {}
      const code = typeof e.code === 'string' ? e.code : undefined
      const msg = typeof e.message === 'string' ? e.message : `forge API ${res.status}`
      throw new ForgeError(mapForgeErrorCode(code, res.status), msg)
    }
    return unwrap(parsed)
  }

  async getRepo(repo: RepoRef): Promise<Repo> {
    const r = (await this.request('GET', repoPath(repo))) as Repo
    return { ...repo, ...r }
  }

  async listPrs(query: ListPrsQuery = {}): Promise<Page<PullRequest>> {
    const q = new URLSearchParams()
    if (query.state) q.set('state', query.state)
    if (query.author) q.set('author', query.author)
    if (query.assignee) q.set('assignee', query.assignee)
    if (query.query) q.set('q', query.query)
    if (query.limit) q.set('limit', String(query.limit))
    if (query.cursor) q.set('cursor', query.cursor)
    const qs = q.toString()
    const path = (query.repo ? `${repoPath(query.repo)}/pulls` : '/pulls') + (qs ? `?${qs}` : '')
    const data = await this.request('GET', path)
    return {
      items: listOf<unknown>(data).map(normalizePr),
      nextCursor: isObj(data) && typeof data.nextCursor === 'string' ? data.nextCursor : null,
    }
  }

  async getPr(ref: PrRef): Promise<PullRequest> {
    return normalizePr(await this.request('GET', prPath(ref)))
  }

  async getDiff(ref: PrRef): Promise<FileDiff[]> {
    return listOf<unknown>(await this.request('GET', `${prPath(ref)}/diff`)).map(normalizeFile)
  }

  async listCommits(ref: PrRef): Promise<Commit[]> {
    return listOf<Commit>(await this.request('GET', `${prPath(ref)}/commits`))
  }

  async listComments(ref: PrRef): Promise<ForgeComment[]> {
    return listOf<ForgeComment>(await this.request('GET', `${prPath(ref)}/comments`))
  }

  async addComment(ref: PrRef, input: AddCommentInput): Promise<ForgeComment> {
    return (await this.request('POST', `${prPath(ref)}/comments`, input, true)) as ForgeComment
  }

  async reviewPr(ref: PrRef, input: ReviewInput): Promise<Review> {
    // Wire shape: snake_case, pinned to the head the reviewer saw (409 head_moved when it changed).
    const body = { verdict: input.verdict, body: input.body, comments: input.comments?.map((c) => ({ ...c, side: c.side === 'left' ? 'LEFT' : 'RIGHT' })), head_sha: input.headSha }
    return (await this.request('POST', `${prPath(ref)}/reviews`, body, true)) as Review
  }

  async validateReview(ref: PrRef, input: { headSha: string; comments: NonNullable<ReviewInput['comments']> }): Promise<ReviewValidation> {
    const comments = input.comments.map((c) => ({ ...c, side: c.side === 'left' ? 'LEFT' : 'RIGHT' }))
    const raw = await this.request('POST', `${prPath(ref)}/reviews/validate`, { head_sha: input.headSha, comments })
    const o = isObj(raw) ? raw : {}
    const rows = (Array.isArray(o.comments) ? o.comments : []).filter(isObj)
    const statuses: CommentCheck[] = input.comments.map((_, i) => {
      const st = rows.find((r) => r.index === i)?.status
      return st === 'outdated' || st === 'invalid_line' ? st : 'ok'
    })
    return { headMoved: o.head_moved === true, statuses }
  }

  async getReviewDraft(ref: PrRef): Promise<ReviewDraft | null> {
    let raw: unknown
    try {
      raw = await this.request('GET', `${prPath(ref)}/review-draft`)
    } catch (e) {
      if (e instanceof ForgeError && e.code === 'not_found') return null
      throw e
    }
    const o = isObj(raw) ? raw : {}
    const d = isObj(o.draft) ? o.draft : {}
    const comments = (Array.isArray(d.comments) ? d.comments : []).filter(isObj).map((c) => ({
      body: String(c.body ?? ''), path: typeof c.path === 'string' ? c.path : undefined, line: typeof c.line === 'number' ? c.line : undefined,
      side: c.side === 'LEFT' || c.side === 'left' ? ('left' as const) : ('right' as const),
    }))
    return { headSha: typeof o.head_sha === 'string' ? o.head_sha : '', comments }
  }

  async saveReviewDraft(ref: PrRef, draft: ReviewDraft): Promise<void> {
    const comments = draft.comments.map((c) => ({ ...c, side: c.side === 'left' ? 'LEFT' : 'RIGHT' }))
    await this.request('PUT', `${prPath(ref)}/review-draft`, { head_sha: draft.headSha, draft: { comments } }, true)
  }

  async deleteReviewDraft(ref: PrRef): Promise<void> {
    await this.request('DELETE', `${prPath(ref)}/review-draft`, undefined, true)
  }

  async getMergePreflight(ref: PrRef): Promise<MergePreflight> {
    const raw = await this.request('GET', `${prPath(ref)}/merge-preflight`)
    const o = isObj(raw) ? raw : {}
    const methods = (Array.isArray(o.methods_allowed) ? o.methods_allowed : []).filter((m): m is MergeMethod => m === 'merge' || m === 'squash' || m === 'rebase')
    const state = o.state === 'ready' || o.state === 'blocked' || o.state === 'pending' ? o.state : 'unknown'
    return {
      state,
      headSha: typeof o.head_sha === 'string' ? o.head_sha : '',
      baseRef: typeof o.base_ref === 'string' ? o.base_ref : '',
      blockers: (Array.isArray(o.blockers) ? o.blockers : []).filter(isObj).map((b) => ({ code: String(b.code ?? ''), message: String(b.message ?? '') })),
      methodsAllowed: methods,
      defaultMethod: methods.find((m) => m === o.default_method),
      behindBy: typeof o.behind_by === 'number' ? o.behind_by : 0,
    }
  }

  async mergePr(ref: PrRef, input: MergeInput): Promise<MergeResult> {
    const result = await this.request('PUT', `${prPath(ref)}/merge`, { method: input.method, head_sha: input.headSha }, true) as MergeResult
    if (!result || result.merged !== true) throw new ForgeError('conflict', result?.message || 'GitHub did not confirm the merge')
    return result
  }

  async assign(ref: PrRef, input: AssignInput): Promise<PullRequest> {
    return normalizePr(await this.request('PUT', `${prPath(ref)}/assignees`, input, true))
  }

  async listPendingPrs(): Promise<PendingPrSummary[]> {
    const raw = await this.request('GET', '/pulls/pending')
    const items = listOf<unknown>(raw).map(normalizePending)
    // The host answers 200 with an empty, degraded list when it cannot read GitHub. That is not "all clear".
    if (items.length === 0 && isObj(raw) && raw.degraded === true) throw new ForgeError('unavailable', 'GitHub data is unavailable right now, so pending pull requests cannot be listed.')
    return items
  }
}

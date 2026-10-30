import { ApiError, createApiClient, type ApiClient } from '@hollis-labs/design-app-runtime'
import type { PlannerClient, PlannerReference, PlannerRevision } from './contracts'
import { PlannerApiError } from './errors'
import type { PlannerBoard, PlannerDatabaseInfo, PlannerDb, PlannerItem, PlannerLink, PlannerOrdered, PlannerSchemaInfo, PlannerSearchHit, PlannerTaskBrief, PlannerWorkstreamTasks, TaskMeta, TaskLane, TaskTransitionResult, ThemeAction, ThemeBoard, ThemeTasks, ThemeTimeline } from './types'
import type { Task, Comment, ListPage } from '@tachyon/torque-client'

export const PLANNER_BASE = '/api/plugins/console/v1/planner'
export const isUUID = (id: string) => /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(id)
function uuid(id: string): string {
  if (!isUUID(id)) throw new PlannerApiError(400, 'A Tachyon UUID is required. Resolve the imported reference before changing it.', 'invalid')
  return id
}
function revision(rev: number): number {
  if (!Number.isSafeInteger(rev) || rev < 1) throw new PlannerApiError(400, 'Reload the record to obtain its current revision.', 'invalid')
  return rev
}
// 'priorities' is the database's earlier name; the server still answers to it.
const databases = new Set(['themes', 'priorities', 'workstreams', 'roadmap', 'ideas', 'decisions', 'risks', 'inbox'])
function dbPath(db: PlannerDb): string {
  if (!databases.has(db)) throw new PlannerApiError(400, 'Unknown Planner database.', 'invalid')
  return `/${db}`
}

/** Explicit host-owned transport. No provider fallback or automatic write retries. */
export class TachyonPlannerClient implements PlannerClient {
  readonly provider = 'tachyon' as const
  private verified = false
  get eventsUrl() { return this.verified ? `${PLANNER_BASE}/events` : undefined }
  private discovery?: Promise<void>
  constructor(private http: ApiClient = createApiClient({ baseUrl: PLANNER_BASE })) {}
  private assertOwned(value: unknown): asserts value is PlannerDatabaseInfo[] {
    if (!Array.isArray(value) || value.length === 0 || !value.every(row => row && typeof row === 'object' && databases.has(row.name) && row.schema === `tachyon/planner/${row.name}@1`)) {
      this.verified = false
      throw new PlannerApiError(503, 'The Tachyon-owned Planner contract is not available. Verification is required before this client can read or write.', 'contract_unavailable')
    }
    this.verified = true
  }
  private async ensureOwned(fresh = false): Promise<void> {
    if (fresh) this.discovery = undefined
    this.discovery ??= this.raw<unknown>('/databases').then(value => this.assertOwned(value)).catch(error => { this.discovery = undefined; this.verified = false; throw error })
    return this.discovery
  }
  private async request<T>(path: string, init?: RequestInit, query?: Record<string, string | number | undefined>, signal?: AbortSignal): Promise<T> {
    if (path !== '/databases') await this.ensureOwned(!!init?.method && init.method !== 'GET')
    signal?.throwIfAborted()
    const value = await this.raw<T>(path, init, query, signal)
    if (path === '/databases') this.assertOwned(value)
    return value
  }
  private async raw<T>(path: string, init?: RequestInit, query?: Record<string, string | number | undefined>, signal?: AbortSignal): Promise<T> {
    try {
      return await this.http.request<T>(path, { cache: 'no-store', ...init }, { query, signal })
    } catch (err) {
      if (err instanceof ApiError) {
        const body = err.data && typeof err.data === 'object' ? err.data as Record<string, unknown> : {}
        throw new PlannerApiError(err.status, typeof body.error === 'string' ? body.error : err.message, typeof body.code === 'string' ? body.code : undefined, body.details as Record<string, unknown> | undefined)
      }
      throw err
    }
  }
  private send<T>(path: string, body: unknown, method = 'POST'): Promise<T> {
    return this.request<T>(path, { method, body: JSON.stringify(body) })
  }
  plannerDatabases = () => this.request<PlannerDatabaseInfo[]>('/databases')
  plannerBoard: PlannerClient['plannerBoard'] = (opts, signal) => this.request<PlannerBoard>('/board', undefined, opts, signal)
  plannerThemeBoard: PlannerClient['plannerThemeBoard'] = (opts, signal) => this.request<ThemeBoard>('/board', undefined, { scope: 'themes', recent_hours: opts?.recent_hours, limit: opts?.limit, include_proposed: opts?.include_proposed ? 'true' : undefined }, signal)
  plannerThemeAction = (id: string, action: ThemeAction, rev: number, args?: { position?: number; reason?: string }) => this.send<unknown>(`/themes/${uuid(id)}/${action}`, { ...args, expected_revision: revision(rev) })
  plannerSchema = (db: PlannerDb) => this.request<PlannerSchemaInfo>(`/schema${dbPath(db)}`)
  plannerList: PlannerClient['plannerList'] = (db, opts, signal) => this.request<PlannerItem[]>(dbPath(db), undefined, { ...opts?.filters, sort: opts?.sort }, signal)
  async plannerGet(db: PlannerDb, id: string): Promise<PlannerItem> {
    const item = await this.request<PlannerItem>(`${dbPath(db)}/${encodeURIComponent(id)}`)
    uuid(item.id)
    revision(item.rev)
    return item
  }
  plannerSearch = (q: string, db?: PlannerDb, signal?: AbortSignal) => this.request<PlannerSearchHit[]>('/search', undefined, { q, db }, signal)
  plannerCreate = (db: PlannerDb, item: Record<string, unknown>, id: string) => this.send<PlannerItem>(dbPath(db), { id: uuid(id), fields: item })
  plannerUpdate = (db: PlannerDb, id: string, patch: Record<string, unknown>, rev: number) => this.send<PlannerItem>(`${dbPath(db)}/${uuid(id)}`, { patch, expected_revision: revision(rev) }, 'PATCH')
  plannerComment = (db: PlannerDb, id: string, text: string, rev: number, kind?: 'comment' | 'decision', opts?: { pin?: boolean }) => this.send<PlannerItem>(`${dbPath(db)}/${uuid(id)}/comments`, { text, kind, ...(opts?.pin === undefined ? {} : { pin: opts.pin }), expected_revision: revision(rev) })
  plannerLinkAdd = (db: PlannerDb, id: string, link: PlannerLink, rev: number) => this.send<PlannerItem>(`${dbPath(db)}/${uuid(id)}/links`, { link, expected_revision: revision(rev) })
  plannerLinkRemove = (db: PlannerDb, id: string, kind: string, ref: string, rev: number) => this.send<PlannerItem>(`${dbPath(db)}/${uuid(id)}/links`, { kind, ref, expected_revision: revision(rev) }, 'DELETE')
  plannerReorder = (db: PlannerDb, items: PlannerRevision[]) => this.send<PlannerOrdered[]>(`${dbPath(db)}/reorder`, { items: items.map(({ id, rev }) => ({ id: uuid(id), expected_revision: revision(rev) })) })
  plannerDecide = (id: string, option: string, rev: number, comment?: string) => this.send<PlannerItem>(`/decisions/${uuid(id)}/decide`, { option, comment, expected_revision: revision(rev) })
  plannerDefer = (id: string, rev: number, note?: string) => this.send<PlannerItem>(`/decisions/${uuid(id)}/defer`, { note, expected_revision: revision(rev) })
  plannerReopen = (id: string, rev: number) => this.send<PlannerItem>(`/decisions/${uuid(id)}/reopen`, { expected_revision: revision(rev) })
  plannerInboxPromote = (id: string, to_db: PlannerDb, rev: number, targetId: string, fields?: Record<string, unknown>) => this.send<{ inbox: PlannerItem; item: PlannerItem }>(`/inbox/${uuid(id)}/promote`, { to_db, target_id: uuid(targetId), fields, expected_revision: revision(rev) })
  plannerPin = (db: PlannerDb, id: string, pinned: boolean, rev: number) => this.send<{ item: { id: string; db: string; rev: number; pinned: boolean; pinned_at?: string } }>(`${dbPath(db)}/${encodeURIComponent(id)}/pin`, { pinned, expected_revision: revision(rev) }, 'PUT')
  plannerInboxDismiss = (id: string, rev: number, note?: string) => this.send<PlannerItem>(`/inbox/${uuid(id)}/dismiss`, { note, expected_revision: revision(rev) })
  async plannerTaskTitles(ids: string[], signal?: AbortSignal): Promise<Record<string, PlannerTaskBrief | null>> {
    const unique = [...new Set(ids)]
    const titles: Record<string, PlannerTaskBrief | null> = {}
    for (let i = 0; i < unique.length; i += 100) {
      Object.assign(titles, await this.request<Record<string, PlannerTaskBrief | null>>('/torque-titles', undefined, { ids: unique.slice(i, i + 100).join(',') }, signal))
    }
    return titles
  }
  plannerWorkstreamTasks = (id: string, signal?: AbortSignal) => this.request<PlannerWorkstreamTasks>(`/workstreams/${uuid(id)}/tasks`, undefined, undefined, signal)
  plannerThemeTasks = (themeId: string, signal?: AbortSignal) => this.request<ThemeTasks>(`/themes/${encodeURIComponent(themeId)}/tasks`, undefined, undefined, signal)
  plannerThemeTimeline = (themeId: string, opts?: { before_seq?: number; limit?: number }, signal?: AbortSignal) => this.request<ThemeTimeline>(`/themes/${encodeURIComponent(themeId)}/timeline`, undefined, { before_seq: opts?.before_seq, limit: opts?.limit }, signal)
  plannerTaskTransition = (task: string, to: TaskLane, expectedStatus: TaskLane, args?: { reason?: string }) => this.send<TaskTransitionResult>(`/tasks/${encodeURIComponent(task)}/transition`, { to, expected_status: expectedStatus, ...(args?.reason ? { reason: args.reason } : {}) })
  plannerTaskMeta = (task: string, meta: { done_summary?: string; pr_cites?: { host: string; owner: string; repo: string; number: number }[] }) => this.send<TaskMeta>(`/tasks/${encodeURIComponent(task)}/meta`, meta, 'PUT')
  getTask = (id: string, signal?: AbortSignal) => this.request<Task>(`/tasks/${encodeURIComponent(id)}`, undefined, undefined, signal)
  listTaskComments: PlannerClient['listTaskComments'] = (id, query, signal) => this.request<ListPage<Comment>>(`/tasks/${encodeURIComponent(id)}/comments`, undefined, query, signal)
  async resolveReference(db: PlannerDb | null, id: string): Promise<PlannerReference> {
    if (!db) {
      const resolved = await this.request<PlannerReference>('/resolve', undefined, { ref: id })
      dbPath(resolved.db)
      uuid(resolved.id)
      return { db: resolved.db, id: resolved.id }
    }
    if (isUUID(id)) return { db, id }
    const item = await this.plannerGet(db, id)
    return { db, id: item.id }
  }

  subscribeEvents: PlannerClient['subscribeEvents'] = (listener) => {
    // Fallback invalidation while the owned SSE stream is disconnected.
    const timer = setInterval(() => listener({ type: 'planner.changed' }), 30_000)
    return () => clearInterval(timer)
  }
}

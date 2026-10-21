import type { PlannerBoard, PlannerDatabaseInfo, PlannerDb, PlannerItem, PlannerLink, PlannerOrdered, PlannerSchemaInfo, PlannerSearchHit, PlannerTaskBrief, PlannerWorkstreamTasks, ThemeAction, ThemeBoard } from './types'
import type { Task, Comment, ListPage } from '@tachyon/torque-client'

export interface PlannerEvent { type: string }
export interface PlannerRevision { id: string; rev: number }
export interface PlannerReference { db: PlannerDb; id: string }

/** The UI depends on this domain boundary, never on a Torque Planner transport. */
export interface PlannerClient {
  readonly provider: 'tachyon' | 'torque'
  readonly eventsUrl?: string
  plannerDatabases(): Promise<PlannerDatabaseInfo[]>
  plannerBoard(opts?: { scope?: 'curated' | 'all'; recent_hours?: number; active_hours?: number; limit?: number }, signal?: AbortSignal): Promise<PlannerBoard>
  plannerThemeBoard(opts?: { recent_hours?: number; limit?: number; include_proposed?: boolean }, signal?: AbortSignal): Promise<ThemeBoard>
  /** A theme lifecycle action. approve and resequence are operator-only on the server. */
  plannerThemeAction(id: string, action: ThemeAction, rev: number, args?: { position?: number; reason?: string }): Promise<unknown>
  plannerSchema(db: PlannerDb): Promise<PlannerSchemaInfo>
  plannerList(db: PlannerDb, opts?: { filters?: Record<string, string>; sort?: string }, signal?: AbortSignal): Promise<PlannerItem[]>
  plannerGet(db: PlannerDb, id: string): Promise<PlannerItem>
  plannerSearch(q: string, db?: PlannerDb, signal?: AbortSignal): Promise<PlannerSearchHit[]>
  plannerCreate(db: PlannerDb, item: Record<string, unknown>, id: string): Promise<PlannerItem>
  plannerUpdate(db: PlannerDb, id: string, patch: Record<string, unknown>, rev: number): Promise<PlannerItem>
  plannerComment(db: PlannerDb, id: string, text: string, rev: number, kind?: 'comment' | 'decision'): Promise<PlannerItem>
  plannerLinkAdd(db: PlannerDb, id: string, link: PlannerLink, rev: number): Promise<PlannerItem>
  plannerLinkRemove(db: PlannerDb, id: string, kind: string, ref: string, rev: number): Promise<PlannerItem>
  plannerReorder(db: PlannerDb, items: PlannerRevision[]): Promise<PlannerOrdered[]>
  plannerDecide(id: string, option: string, rev: number, comment?: string): Promise<PlannerItem>
  plannerDefer(id: string, rev: number, note?: string): Promise<PlannerItem>
  plannerReopen(id: string, rev: number): Promise<PlannerItem>
  plannerInboxPromote(id: string, to: PlannerDb, rev: number, targetId: string, fields?: Record<string, unknown>): Promise<{ inbox: PlannerItem; item: PlannerItem }>
  plannerInboxDismiss(id: string, rev: number, note?: string): Promise<PlannerItem>
  plannerTaskTitles(ids: string[], signal?: AbortSignal): Promise<Record<string, PlannerTaskBrief | null>>
  plannerWorkstreamTasks(id: string, signal?: AbortSignal): Promise<PlannerWorkstreamTasks>
  getTask(id: string, signal?: AbortSignal): Promise<Task>
  listTaskComments(id: string, query?: { limit?: number; sort_by?: string; sort_dir?: 'asc' | 'desc' }, signal?: AbortSignal): Promise<ListPage<Comment>>
  resolveReference(db: PlannerDb | null, id: string): Promise<PlannerReference>
  subscribeEvents(listener: (event: PlannerEvent) => void): () => void
  enable?(): Promise<void>
}

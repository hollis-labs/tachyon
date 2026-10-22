import { createContext, useContext, useEffect, useSyncExternalStore } from 'react'
import type { Comment, ListPage, Task } from '@tachyon/torque-client'

/** How long a fetched task stays fresh. Hover cards and the drawer share it. */
export const TASK_DETAIL_TTL_MS = 60_000
const MAX_ENTRIES = 100
const COMMENT_PAGE = 25

export interface TaskDetailApi {
  getTask(id: string, signal?: AbortSignal): Promise<Task>
  listComments(entityType: string, entityID: string, query?: { limit?: number; sort_by?: string; sort_dir?: 'asc' | 'desc'; include_total?: boolean }, signal?: AbortSignal): Promise<ListPage<Comment>>
}

export interface TaskDetailEntry {
  task: Task | null
  /** Newest first. */
  comments: Comment[]
  /** Total comment count when Torque reports it; otherwise the loaded count (see `moreComments`). */
  commentCount: number
  /** True when more comments exist than the ones loaded. */
  moreComments: boolean
  /** Comments could not be read though the task could. */
  commentsFailed: boolean
  phase: 'loading' | 'ready' | 'error'
  error?: string
  /** When the data was fetched; stale data stays visible while a refresh runs. */
  fetchedAt: number
}

export interface TaskDetailCache {
  get(id: string): TaskDetailEntry | undefined
  /** Fetch unless a fresh entry or a request is already there. `force` ignores freshness. */
  ensure(id: string, opts?: { force?: boolean }): void
  invalidate(id: string): void
  subscribe(listener: () => void): () => void
}

/**
 * A small read-through cache for Torque tasks and their latest comments. Torque
 * stays the source of truth: entries expire after the TTL, one request serves
 * every subscriber, and nothing is persisted.
 */
export function createTaskDetailCache(api: TaskDetailApi, opts: { ttlMs?: number; now?: () => number } = {}): TaskDetailCache {
  const ttl = opts.ttlMs ?? TASK_DETAIL_TTL_MS
  const now = opts.now ?? Date.now
  const entries = new Map<string, TaskDetailEntry>()
  const inflight = new Map<string, number>()
  const listeners = new Set<() => void>()
  let generation = 0
  const emit = () => listeners.forEach((l) => l())

  const set = (id: string, entry: TaskDetailEntry) => {
    entries.delete(id)
    entries.set(id, entry)
    // Drop the least recently written entries; the cache is a convenience, not a store.
    while (entries.size > MAX_ENTRIES) {
      const oldest = entries.keys().next().value
      if (oldest === undefined) break
      entries.delete(oldest)
    }
    emit()
  }

  async function load(id: string, token: number) {
    const prev = entries.get(id)
    try {
      const [task, page] = await Promise.all([
        api.getTask(id),
        api.listComments('task', id, { limit: COMMENT_PAGE, sort_by: 'created_at', sort_dir: 'desc', include_total: true }).then(
          (p) => p,
          () => null,
        ),
      ])
      if (inflight.get(id) !== token) return
      const comments = page?.items ?? []
      const total = page?.meta.total
      set(id, {
        task,
        comments,
        commentCount: typeof total === 'number' ? total : comments.length,
        moreComments: typeof total === 'number' ? total > comments.length : Boolean(page?.meta.has_more),
        commentsFailed: page === null,
        phase: 'ready',
        fetchedAt: now(),
      })
    } catch (err) {
      if (inflight.get(id) !== token) return
      const message = err instanceof Error ? err.message : 'Failed to load the task'
      set(id, { task: prev?.task ?? null, comments: prev?.comments ?? [], commentCount: prev?.commentCount ?? 0, moreComments: prev?.moreComments ?? false, commentsFailed: prev?.commentsFailed ?? false, phase: 'error', error: message, fetchedAt: prev?.fetchedAt ?? 0 })
    } finally {
      if (inflight.get(id) === token) inflight.delete(id)
    }
  }

  return {
    get: (id) => entries.get(id),
    ensure(id, o) {
      const cur = entries.get(id)
      if (inflight.has(id)) return
      if (!o?.force && cur && cur.phase === 'ready' && now() - cur.fetchedAt < ttl) return
      const token = ++generation
      inflight.set(id, token)
      // Keep stale data on screen while it refreshes.
      set(id, cur ? { ...cur, phase: 'loading' } : { task: null, comments: [], commentCount: 0, moreComments: false, commentsFailed: false, phase: 'loading', fetchedAt: 0 })
      void load(id, token)
    },
    invalidate(id) {
      inflight.delete(id)
      entries.delete(id)
      emit()
    },
    subscribe(listener) {
      listeners.add(listener)
      return () => {
        listeners.delete(listener)
      }
    },
  }
}

export const TaskDetailContext = createContext<TaskDetailCache | null>(null)

/** The task for `id`, fetched on mount and kept fresh within the TTL. Undefined outside a provider. */
export function useTaskDetail(id: string): { entry: TaskDetailEntry | undefined; refresh: () => void; available: boolean } {
  const cache = useContext(TaskDetailContext)
  const entry = useSyncExternalStore(
    (l) => cache?.subscribe(l) ?? (() => undefined),
    () => cache?.get(id),
    () => undefined,
  )
  useEffect(() => {
    cache?.ensure(id)
  }, [cache, id])
  return { entry, refresh: () => cache?.ensure(id, { force: true }), available: cache !== null }
}

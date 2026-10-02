import type { WorkList } from "./client"
import { invokeVerb } from "./verbs"

export interface WorkFilters {
  status?: string
  project_id?: string
  /** Comma-separated provider tag filters. */
  tags?: string
  limit?: number
  offset?: number
}

export const workApi = {
  list: (filters: WorkFilters = {}) => invokeVerb<WorkList>("work_list", filters),
  search: (query: string) => invokeVerb<WorkList>("work_search", { query }),
}

export const ACTIVE_STATUSES = ["backlog", "todo", "queued", "doing", "review", "blocked", "paused"]
export const CLOSED_STATUSES = ["done", "archived", "abandoned", "cancelled"]

/** Offset pages can drift during mutations; never infer an exact total from loaded rows. */
export function workPageInfo(page: WorkList, offset: number, filters: WorkFilters = {}) {
  if (!Array.isArray(page.tasks)) throw new Error("Task list response is incomplete.")
  if (filters.status && page.tasks.some((task) => task.status !== filters.status))
    throw new Error("Provider did not apply the requested status filter.")
  if (filters.project_id && page.tasks.some((task) => task.project_id !== filters.project_id))
    throw new Error("Provider did not apply the requested project filter.")
  if (typeof page.has_more !== "boolean") throw new Error("Task page metadata is incomplete.")
  if (
    page.has_more &&
    (page.next_offset == null || !Number.isInteger(page.next_offset) || page.next_offset <= offset)
  )
    throw new Error("Task page continuation is invalid. Try refreshing.")
  const end = offset + page.tasks.length
  const total =
    Number.isInteger(page.total) &&
    page.total >= end &&
    (page.has_more ? page.total > (page.next_offset as number) : page.total === end)
      ? page.total
      : undefined
  return {
    total,
    hasMore: page.has_more,
    next: page.has_more ? (page.next_offset as number) : undefined,
    more: total !== undefined && page.has_more ? total - (page.next_offset as number) : undefined,
  }
}

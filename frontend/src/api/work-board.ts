import type { WorkList } from "./client"
import { invokeVerb } from "./verbs"

export interface BoardFilters {
  status: string
  project_id?: string
  limit: number
  offset: number
}
export const workBoardApi = {
  list: (filters: BoardFilters) => invokeVerb<WorkList>("work_list", filters),
}
export const ACTIVE_STATUSES = ["backlog", "todo", "queued", "doing", "review", "blocked", "paused"]
export const CLOSED_STATUSES = ["done", "archived", "abandoned", "cancelled"]

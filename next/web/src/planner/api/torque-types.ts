// The Torque shapes the planner client names (from the old @tachyon/torque-client, which the port
// does not carry). Only the fields the planner reads are typed; the rest pass through.

export interface Task {
  id: string
  title: string
  description: string
  status: string
  priority: number
  [field: string]: unknown
}

export interface Comment {
  id: number
  entity_type: string
  entity_id: string
  author: string
  content: string
  created_at: string
}

export interface ListPage<T> {
  items: T[]
  meta: {
    returned: number
    limit: number
    has_more: boolean
    next_cursor: string | null
    total?: number
    [field: string]: unknown
  }
}

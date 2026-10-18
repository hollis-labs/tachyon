// Planner view contracts. Task execution remains an external Torque concern.
export type PlannerDb =
  | 'priorities'
  | 'workstreams'
  | 'roadmap'
  | 'ideas'
  | 'decisions'
  | 'risks'
  | 'inbox'

export interface PlannerComment {
  id: string
  author: string
  text: string
  created: string
  kind?: 'comment' | 'decision'
}

/** An external pointer: a Torque task, a PR, a URL, a file. */
export interface PlannerLink {
  kind: string
  ref: string
  label?: string
}

export interface PlannerOption {
  id: string
  label: string
  summary?: string
  pros?: string
  cons?: string
  proposed_by?: string
  recommended?: boolean
}

/**
 * One planner item. Only the fields every database shares are typed; the
 * rest are per-database (see the schema from `plannerSchema`) and may carry
 * fields the GUI does not know.
 */
export interface PlannerItem {
  /** Explicit imported/local reference resolution; UUIDs never imply a database. */
  references?: Record<string, { db: PlannerDb; id: string }>
  id: string
  title: string
  rev: number
  status?: string
  kind?: string
  tldr?: string
  notes?: string
  priority?: number
  order?: number
  created?: string
  updated?: string
  author?: string
  related_ids?: string[]
  links?: PlannerLink[]
  comments?: PlannerComment[]
  options?: PlannerOption[]
  [field: string]: unknown
}

export interface PlannerDatabaseInfo {
  name: PlannerDb
  prefix: string
  schema: string
  updated: string
  count: number
}

export interface PlannerFieldSchema {
  type?: string | string[]
  enum?: string[]
  description?: string
  items?: string
  ref?: string
  minimum?: number
  maximum?: number
}

export interface PlannerSchemaInfo {
  db: PlannerDb
  id: string
  description: string
  required: string[]
  fields: Record<string, PlannerFieldSchema>
  enums: Record<string, string[]>
  link_kinds: string[]
}

export interface PlannerSearchHit {
  db: PlannerDb
  id: string
  title: string
  status?: string
}

export interface PlannerOrdered {
  id: string
  order: number
  rev: number
}

export type PlannerBoardSectionKey = 'in_flight' | 'landed' | 'pre_flight' | 'holds'

/** One line of the departure board. Torque tasks have source "torque"; planner rows carry their database. */
export interface PlannerBoardRow {
  source: 'torque' | 'roadmap' | 'decision' | 'inbox' | 'risk' | 'workstream'
  id: string
  title: string
  status: string
  priority?: number
  workstream?: string
  updated: string
  db?: PlannerDb
  /** Workstream rows of the curated board only. task_counts are counts per Torque status, never task rows. */
  phase?: string
  owner?: string
  tldr?: string
  next_step?: string
  blockers?: string[]
  tags?: string[]
  task_counts?: Record<string, number>
}

/** curated: the workstreams tagged `board`; all: the activity feed with Torque tasks. */
export type PlannerBoardScope = 'curated' | 'all'

export interface PlannerBoard {
  scope?: PlannerBoardScope
  generated_at: string
  recent_hours: number
  active_hours: number
  limit: number
  sections: Record<PlannerBoardSectionKey, PlannerBoardRow[]>
  counts: Record<PlannerBoardSectionKey, number>
  totals: Record<PlannerBoardSectionKey, number>
  notices: string[]
}

/** A Torque task as the planner shows it beside a reference. */
export interface PlannerTaskBrief {
  id: string
  title: string
  status: string
  priority: number
  updated_at: string
}

export interface PlannerWorkstreamTasks {
  workstream_id: string
  torque_project_ids: string[]
  torque_tag: string
  counts: Record<string, number>
  total: number
  open: PlannerTaskBrief[]
  open_total: number
  note?: string
}

// ---------- suggestions (the enrichment ledger) ----------

/** An open tag suggestion: a model's answer, kept in the ledger until a person decides. */
export interface Suggestion {
  id: number
  subject_type: string
  subject_id: string
  set_id: string
  question_key: string
  answer_type: 'noul' | 'choice' | 'score'
  choice?: string
  noul?: number
  confidence: number
  suggested: string[]
  provider: string
  model: string
  disposition: string
  created_at: string
  task_title?: string
  task_tags?: string[]
}

export interface JudgeStatus {
  feature_enabled: boolean
  decider_configured: boolean
  decider?: string
  active: boolean
}

/** Per set: question key → disposition → rows. */
export type JudgeSummary = Record<string, Record<string, Record<string, number>>>

export interface JudgeBulkResult {
  decided: number
  failed?: string[]
}

// Planner view contracts. Task execution remains an external Torque concern.
export type PlannerDb =
  | 'themes'
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

// ---------- the flight board (themes) ----------

/**
 * Display sections of the flight board. They are derived from a theme's stored
 * status and its task activity and are never stored: in_flight is approved with
 * work under way, cleared is approved and waiting its turn in the sequence.
 */
export type ThemeSectionKey = 'in_flight' | 'cleared' | 'queued' | 'parked' | 'landed' | 'proposed'

/** done/total over the tasks under a theme, computed by the server at read time. */
export interface ThemeProgress {
  available: boolean
  done?: number
  total?: number
  open?: number
  counts?: Record<string, number>
  linked?: boolean
  note?: string
  /** Directly linked task ids Torque did not return. */
  missing?: number
  as_of?: string
}

export interface ThemeRow {
  source: 'theme'
  db: 'themes'
  id: string
  /** The record revision, sent back with an action so a stale view cannot overwrite a newer one. */
  rev: number
  /** Pinned on the server (pin is item state). */
  pinned?: boolean
  title: string
  status: string
  stored_status?: string
  section: ThemeSectionKey
  updated: string
  priority?: number
  order?: number
  /** Place in the whole approved sequence (1 first), counted by the server across in flight and cleared. */
  rank?: number
  tldr?: string
  state?: string
  tags?: string[]
  accountable?: string
  executing_owner?: string
  approved_by?: string
  approved_at?: string
  last_verified?: string
  depends_on?: string[]
  decision_ids?: string[]
  /** Linked decisions still waiting, by title. */
  pending_decisions?: { id: string; title: string; created?: string }[]
  risk_ids?: string[]
  blocked_count: number
  first_blocker?: string
  next_step?: string
  unmet_dependencies: number
  idle_tasks?: number
  progress: ThemeProgress
}

export interface ThemeBoard {
  scope: 'themes'
  generated_at: string
  recent_hours: number
  limit: number
  sections: Record<ThemeSectionKey, ThemeRow[]>
  counts: Record<ThemeSectionKey, number>
  totals: Record<ThemeSectionKey, number>
  notices: string[]
  progress: { done: number; total: number; note?: string }
}

export type ThemeAction = 'ready' | 'approve' | 'park' | 'unpark' | 'finish' | 'archive' | 'resequence'

export type TaskLane = 'todo' | 'doing' | 'review' | 'blocked' | 'done'
export interface PrCite { host: string; owner: string; repo: string; number: number; source?: 'manual' | 'detected' }
export interface TaskAssignee { assignment_id: string; agent_id?: string; team_id?: string; label?: string; state: string }
/** A Torque task as the planner shows it on a theme board. */
export interface ThemeTask {
  id: string
  title: string
  status: TaskLane
  priority?: string
  updated_at: string
  assignees: TaskAssignee[]
  pr_cites: PrCite[]
  done_summary?: string
  blocked_reason?: string
}
export interface ThemeTasks {
  theme_id: string
  counts: Record<TaskLane | 'total', number>
  lanes: Record<TaskLane, ThemeTask[]>
}
export interface TaskTransitionResult {
  task: { id: string; status: TaskLane; previous: TaskLane }
  event?: { seq: number; kind: string }
  undo: { to: TaskLane; expected_status: TaskLane; window_seconds: number }
}
export interface TaskMeta {
  task: string
  done_summary?: string
  pr_cites?: PrCite[]
}

export type MomentumLabel = 'rising' | 'steady' | 'cooling' | 'stalled'
export interface TimelineEntry {
  seq: number
  at: string
  kind: 'task' | 'pr' | 'decision' | 'blocker' | 'comment' | 'workstream' | 'lifecycle' | 'link'
  text: string
  refs: { kind: string; ref: string }[]
  delta?: { done: number; doing: number; review: number; blocked: number }
  arrow?: 'up' | 'flat' | 'down'
}
export interface ThemeTimeline {
  milestones: { key: 'approved' | 'first_done' | 'half_done'; state: 'reached' | 'next' | 'pending'; at?: string }[]
  momentum: { label: MomentumLabel; score: number; text: string }
  events: TimelineEntry[]
  next_before_seq: number | null
}

export type NeedYouReason = 'review' | 'decide' | 'blocked' | 'stale'
/** Items that need the operator, by reason and lens. */
export interface NeedYou { total: number; by_reason: Partial<Record<NeedYouReason, number>>; by_lens: Record<string, number>; items: { db: string; id: string; reasons: NeedYouReason[] }[]; stale_days: number }
/** A typed relation, stored on the source item. */
export type RelationKind = 'needs' | 'delivers' | 'mitigates' | 'relates'
export interface PlannerRef { db: PlannerDb; id: string }
export interface RelationOut { kind: RelationKind; to: PlannerRef; title: string; status?: string }
export interface RelationIn { kind: RelationKind; from: PlannerRef; title: string; status?: string }
export interface ExternalLink { kind: string; ref: string; label: string }
export interface LinksView { outbound: RelationOut[]; inbound: RelationIn[]; external: ExternalLink[] }
export interface RelationResult { item: { id: string; db: PlannerDb; rev?: number; revision?: number }; links_view: LinksView }

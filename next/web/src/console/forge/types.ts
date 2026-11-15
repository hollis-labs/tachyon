// Forge-neutral contract the Agent Console GUI codes to. Aligned with the
// Cerberus forge vocabulary (RepoRef, CheckoutRef, ForgeState; CW-20261004-0048)
// so a Cerberus forge plugin can replace the interim adapter without GUI
// changes. INTERIM: see BACKEND_NEEDS.md. Types are additive/tolerant: unknown
// string unions are widened with `(string & {})`, and most fields are optional.

/** Open string union: known literals autocomplete, unknown values still typecheck. */
export type Open<T extends string> = T | (string & {})

export type ForgeHost = Open<'github' | 'gitlab' | 'gitea'>

/** Host-neutral repository identity. */
export interface RepoRef {
  host: ForgeHost
  owner: string
  name: string
}

/** A working copy of a repo at a branch/commit (maps to an agent worktree). */
export interface CheckoutRef {
  repo: RepoRef
  branch: string
  sha?: string
  /** Local path, when the checkout is on a machine the agent owns. */
  path?: string
  machine?: string
}

/** Identifies one pull/merge request. */
export interface PrRef {
  repo: RepoRef
  number: number
}

export interface Repo extends RepoRef {
  description?: string
  defaultBranch: string
  private?: boolean
  stars?: number
  openIssues?: number
  updatedAt?: string
  url?: string
}

/** Coarse forge-side state of a PR (ForgeState). */
export type ForgeState = Open<'open' | 'closed' | 'merged' | 'draft'>

export type MergeableState = Open<'mergeable' | 'conflicting' | 'blocked' | 'unknown'>

export interface ForgeUser {
  login: string
  name?: string
  avatarUrl?: string
  /** True when the account is an agent/bot rather than a human. */
  isAgent?: boolean
}

export type CheckStatus = Open<'queued' | 'in_progress' | 'completed'>
export type CheckConclusion = Open<
  'success' | 'failure' | 'neutral' | 'cancelled' | 'skipped' | 'timed_out' | 'action_required'
>

export interface CheckRun {
  id: string
  name: string
  status: CheckStatus
  conclusion?: CheckConclusion
  startedAt?: string
  completedAt?: string
  url?: string
}

export type ReviewVerdict = Open<'approve' | 'request_changes' | 'comment'>
export type ReviewState = Open<'approved' | 'changes_requested' | 'commented' | 'dismissed' | 'pending'>

export interface Review {
  id: string
  author: ForgeUser
  state: ReviewState
  body?: string
  submittedAt?: string
  commitSha?: string
}

export interface PullRequest {
  ref: PrRef
  title: string
  body?: string
  state: ForgeState
  draft?: boolean
  author: ForgeUser
  assignees: ForgeUser[]
  reviewers?: ForgeUser[]
  labels?: string[]
  baseBranch: string
  headBranch: string
  headSha?: string
  mergeable: MergeableState
  checks: CheckRun[]
  reviews: Review[]
  additions?: number
  deletions?: number
  changedFiles?: number
  commentCount?: number
  createdAt: string
  updatedAt: string
  mergedAt?: string
  url?: string
  /** Optional link back to the Torque task / agent that owns this PR. */
  taskId?: string
  agentId?: string
}

export interface Commit {
  sha: string
  message: string
  author: ForgeUser
  authoredAt: string
  parents?: string[]
  url?: string
}

export type DiffLineKind = 'context' | 'add' | 'del'

export interface DiffLine {
  kind: DiffLineKind
  text: string
  oldLine?: number
  newLine?: number
}

export interface DiffHunk {
  oldStart: number
  oldLines: number
  newStart: number
  newLines: number
  header?: string
  lines: DiffLine[]
}

export type FileChangeStatus = Open<'added' | 'modified' | 'removed' | 'renamed'>

export interface FileDiff {
  path: string
  oldPath?: string
  status: FileChangeStatus
  additions: number
  deletions: number
  binary?: boolean
  /** Set when the forge omitted the patch (too large). */
  truncated?: boolean
  hunks: DiffHunk[]
}

export type CommentSide = 'left' | 'right'

interface CommentBase {
  id: string
  author: ForgeUser
  body: string
  createdAt: string
  updatedAt?: string
  url?: string
}

/** Conversation-level comment on the PR. */
export interface IssueComment extends CommentBase {
  kind: 'issue'
}

/** Inline comment anchored to a diff line. */
export interface ReviewComment extends CommentBase {
  kind: 'review'
  path: string
  line?: number
  side?: CommentSide
  commitSha?: string
  /** Id of the comment this replies to (thread root). */
  inReplyTo?: string
  resolved?: boolean
  reviewId?: string
}

export type ForgeComment = IssueComment | ReviewComment

/** Row for the pending-PR inbox (G4). */
export interface PendingPrSummary {
  ref: PrRef
  title: string
  author: ForgeUser
  assignees: ForgeUser[]
  state: ForgeState
  /** Why it needs attention. */
  reasons: Open<'review_requested' | 'checks_failed' | 'changes_requested' | 'conflicting' | 'unassigned' | 'stale'>[]
  checksSummary?: { passed: number; failed: number; pending: number }
  reviewSummary?: { approved: number; changesRequested: number }
  mergeable: MergeableState
  updatedAt: string
  additions?: number
  deletions?: number
  /** Draft PRs show a Draft chip in the inbox. */
  draft?: boolean
  changedFiles?: number
  url?: string
}

// ---- inputs -------------------------------------------------------------

export interface ListPrsQuery {
  repo?: RepoRef
  state?: 'open' | 'closed' | 'merged' | 'all'
  author?: string
  assignee?: string
  query?: string
  limit?: number
  cursor?: string
}

export interface Page<T> {
  items: T[]
  nextCursor?: string | null
}

export interface AddCommentInput {
  body: string
  /** Omit for a conversation comment; set to anchor inline. */
  path?: string
  line?: number
  side?: CommentSide
  inReplyTo?: string
}

export interface ReviewInput {
  verdict: ReviewVerdict
  body?: string
  /** Head commit the reviewer saw; the forge refuses with head_moved when it changed. */
  headSha?: string
  comments?: Omit<AddCommentInput, 'inReplyTo'>[]
}

export type CommentCheck = 'ok' | 'outdated' | 'invalid_line'
/** POST reviews/validate: whether queued inline comments still land on the current head. */
export interface ReviewValidation { headMoved: boolean; statuses: CommentCheck[] }

/** Saved review in progress: the queued inline comments, pinned to the head they were written on. */
export interface ReviewDraft { headSha: string; comments: NonNullable<ReviewInput['comments']> }

/** GET status: when the forge cache last refreshed and whether the answer is degraded (served from cache). */
export interface ForgeStatus { lastRefreshMs: number | null; degraded: boolean }

/** One forge write recorded server-side (GET /audit). Never carries a body. */
export interface AuditEntry { id: number; at: number; op: string; prKey: string; actor: string; outcome: string; errorCode?: string; githubRef?: string }

export interface AssignInput {
  /** Full replacement set of assignee logins. */
  assignees: string[]
}

export type ForgeErrorCode = Open<'not_found' | 'invalid_args' | 'unauthorized' | 'unavailable' | 'conflict'>

export class ForgeError extends Error {
  code: ForgeErrorCode
  constructor(code: ForgeErrorCode, message: string) {
    super(message)
    this.name = 'ForgeError'
    this.code = code
  }
}

// ---- helpers ------------------------------------------------------------

export function repoKey(r: RepoRef): string {
  return `${r.host}:${r.owner}/${r.name}`
}

export function prKey(p: PrRef): string {
  return `${repoKey(p.repo)}#${p.number}`
}

export function sameRepo(a: RepoRef, b: RepoRef): boolean {
  return repoKey(a) === repoKey(b)
}

// ---- effect classes -----------------------------------------------------

/** Mirrors the Cerberus connector effect classes (pkg/connector Effect). */
export type ForgeEffect = Open<'read' | 'read_sensitive' | 'write' | 'lifecycle' | 'destructive' | 'exec'>

/**
 * Effect class of every ForgeClient method. Anything other than `read` is an
 * acknowledged operation on the Cerberus side and, per D2, should sit behind
 * IdentityConfirm in the GUI when it authorises a change.
 */
export const FORGE_OP_EFFECTS = {
  getRepo: 'read',
  listPrs: 'read',
  getPr: 'read',
  getDiff: 'read',
  listCommits: 'read',
  listComments: 'read',
  listPendingPrs: 'read',
  addComment: 'write',
  reviewPr: 'write',
  assign: 'write',
} as const satisfies Record<string, ForgeEffect>

export type MergeMethod = 'merge' | 'squash' | 'rebase'
export interface MergeInput { method: MergeMethod; headSha: string }
export type MergeBlockerCode = 'draft' | 'closed' | 'conflicts' | 'checks_failing' | 'checks_pending' | 'changes_requested' | 'review_required' | 'behind_base' | 'branch_protection' | 'head_moved' | 'unknown_mergeable'
export interface MergeBlocker { code: MergeBlockerCode | (string & {}); message: string }
/** GET merge-preflight: what a merge would do right now and what blocks it. */
export interface MergePreflight {
  state: 'ready' | 'blocked' | 'pending' | 'unknown'
  headSha: string
  baseRef: string
  blockers: MergeBlocker[]
  methodsAllowed: MergeMethod[]
  defaultMethod?: MergeMethod
  behindBy: number
}
export interface MergeResult { merged: true; sha: string; message: string }

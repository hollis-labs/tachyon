import { AlertTriangle, MessageSquare, RefreshCw } from 'lucide-react'
import { MetaList, Skeleton, type MetaItem } from '@hollis-labs/design-components'
import type { Comment, Task } from '@tachyon/torque-client'
import { ago } from '@console/lib/agent-activity'
import { registerRefDetail, registerRefHover, type RefDetailProps } from './extensions'
import { LinkText } from './LinkText'
import { useTaskDetail, type TaskDetailEntry } from './taskDetail'

const LABEL = 'text-caption uppercase tracking-label text-fg-faint'
const CHIP = 'inline-flex items-center border border-border px-1.5 py-px font-mono text-caption text-fg-secondary'
const BTN = 'inline-flex items-center gap-1.5 border border-border px-2 py-1 font-mono text-caption uppercase tracking-label text-fg-secondary transition-colors hover:border-fg-faint hover:bg-surface-hover focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring'

function stamp(iso: string | undefined, now: number): string {
  const t = iso ? Date.parse(iso) : NaN
  return Number.isFinite(t) ? ago(t, now) : '-'
}
function exact(iso: string | undefined): string | undefined {
  const t = iso ? Date.parse(iso) : NaN
  return Number.isFinite(t) ? new Date(t).toLocaleString() : undefined
}
const commentLabel = (e: TaskDetailEntry) => `${e.commentCount}${e.moreComments && e.commentCount === e.comments.length ? '+' : ''}`

function Meta({ task, entry, now }: { task: Task; entry: TaskDetailEntry; now: number }) {
  const items: MetaItem[] = [
    { label: 'Priority', value: `P${task.priority}` },
    { label: 'Comments', value: entry.commentsFailed ? 'unavailable' : commentLabel(entry) },
    { label: 'Updated', value: <time dateTime={task.updated_at} title={exact(task.updated_at)}>{stamp(task.updated_at, now)}</time> },
    { label: 'Created', value: <time dateTime={task.created_at} title={exact(task.created_at)}>{stamp(task.created_at, now)}</time> },
    { label: 'Executor', value: task.executor || '-' },
    { label: 'Kind', value: task.kind || '-' },
  ]
  if (task.project_id) items.push({ label: 'Project', value: <span className="font-mono">{task.project_id}</span> })
  if (task.epic_id) items.push({ label: 'Epic', value: <span className="font-mono">{task.epic_id}</span> })
  if (task.sprint_id) items.push({ label: 'Sprint', value: <span className="font-mono">{task.sprint_id}</span> })
  if (task.depends_on?.length) {
    items.push({ label: 'Depends on', value: <span className="font-mono"><LinkText surface="task-description" text={task.depends_on.join(' ')} /></span> })
  }
  return <MetaList items={items} columns={2} />
}

function CommentItem({ c, now }: { c: Comment; now: number }) {
  return (
    <li className="border border-border p-2.5">
      <div className="mb-1 flex items-center gap-2 text-caption text-fg-faint">
        <span className="font-medium text-fg-secondary">{c.author || 'unknown'}</span>
        <time dateTime={c.created_at} title={exact(c.created_at)}>{stamp(c.created_at, now)}</time>
      </div>
      <p className="whitespace-pre-wrap text-control leading-relaxed text-fg [overflow-wrap:anywhere]">
        <LinkText surface="task-comments" text={c.content} />
      </p>
    </li>
  )
}

/** Drawer body for a Torque task: description, key metadata and the latest comments, read from Torque (cached). */
export function TaskDrawerPane({ refId }: RefDetailProps) {
  const { entry, refresh, available } = useTaskDetail(refId.canonical)
  const now = Date.now()
  if (!available) return <p className="text-control text-fg-faint">Task details are not available here.</p>
  if (!entry || (entry.phase === 'loading' && !entry.task)) {
    return (
      <div className="space-y-2" aria-label="Loading task" aria-busy="true">
        <Skeleton className="h-3 w-full" />
        <Skeleton className="h-3 w-5/6" />
        <Skeleton className="h-3 w-2/3" />
      </div>
    )
  }
  if (!entry.task) {
    return (
      <div className="flex items-start gap-2 text-control text-destructive" role="alert">
        <AlertTriangle className="mt-px h-3.5 w-3.5 shrink-0" />
        <span className="min-w-0 flex-1 [overflow-wrap:anywhere]">Could not load the task{entry.error ? `: ${entry.error}` : ''}</span>
        <button type="button" className={BTN} onClick={refresh}><RefreshCw className="h-3 w-3" />Retry</button>
      </div>
    )
  }
  const task = entry.task
  return (
    <div className="flex min-w-0 flex-col gap-4" data-testid="task-detail">
      <section aria-label="Description">
        <h3 className={LABEL}>Description</h3>
        {task.description ? (
          <p className="mt-1.5 whitespace-pre-wrap text-control leading-relaxed text-fg [overflow-wrap:anywhere]">
            <LinkText surface="task-description" text={task.description} />
          </p>
        ) : (
          <p className="mt-1.5 text-control text-fg-faint">No description.</p>
        )}
        {task.blocked_reason && (
          <p className="mt-2 border border-warning px-2 py-1.5 text-control text-warning [overflow-wrap:anywhere]">Blocked: {task.blocked_reason}</p>
        )}
      </section>

      <section aria-label="Details" className="border-t border-border pt-3">
        <Meta task={task} entry={entry} now={now} />
        {task.tags.length > 0 && (
          <div className="mt-3 flex flex-wrap gap-1.5" aria-label="Tags">
            {task.tags.map((t) => <span key={t.slug} className={CHIP}>{t.slug}</span>)}
          </div>
        )}
      </section>

      <section aria-label="Comments" className="border-t border-border pt-3">
        <h3 className={LABEL}>Comments{entry.commentsFailed ? '' : ` · ${commentLabel(entry)}`}</h3>
        {entry.commentsFailed ? (
          <p className="mt-1.5 text-control text-fg-faint">Comments could not be loaded.</p>
        ) : entry.comments.length === 0 ? (
          <p className="mt-1.5 text-control text-fg-faint">No comments yet.</p>
        ) : (
          <>
            <ul className="mt-2 flex flex-col gap-2">{entry.comments.map((c) => <CommentItem key={c.id} c={c} now={now} />)}</ul>
            {entry.moreComments && <p className="mt-2 text-caption text-fg-faint">Showing the latest {entry.comments.length} of {entry.commentCount}.</p>}
          </>
        )}
      </section>

      <div className="flex items-center gap-3 border-t border-border pt-3">
        <button type="button" className={BTN} onClick={refresh} disabled={entry.phase === 'loading'}>
          <RefreshCw className={`h-3 w-3 ${entry.phase === 'loading' ? 'animate-spin' : ''}`} />
          Refresh
        </button>
        <span className="font-mono text-caption text-fg-faint">fetched {stamp(new Date(entry.fetchedAt).toISOString(), now)}</span>
        {entry.phase === 'error' && <span className="text-caption text-destructive" role="alert">Refresh failed; showing the last copy.</span>}
      </div>
    </div>
  )
}

/** Hovercard body for a Torque task: the few facts worth a glance (status is in the header). */
export function TaskHoverMeta({ refId, card }: RefDetailProps) {
  const { entry } = useTaskDetail(refId.canonical)
  const task = entry?.task
  const now = Date.now()
  if (!task) {
    return entry?.phase === 'error'
      ? <p className="mt-3 text-control text-fg-faint">Details unavailable right now.</p>
      : <div className="mt-3 space-y-1.5" aria-hidden><Skeleton className="h-2 w-3/4" /><Skeleton className="h-2 w-1/2" /></div>
  }
  const description = task.description.trim()
  return (
    <>
      {description && <p className="mt-3 line-clamp-3 whitespace-pre-wrap text-control leading-relaxed text-fg-secondary [overflow-wrap:anywhere]">{description}</p>}
      <dl className="mt-3 grid grid-cols-3 gap-x-3 border-t border-border pt-3" data-testid="task-hover-meta">
        <div className="min-w-0">
          <dt className={LABEL}>Priority</dt>
          <dd className="mt-0.5 text-control text-fg">P{task.priority}</dd>
        </div>
        <div className="min-w-0">
          <dt className={LABEL}>Comments</dt>
          <dd className="mt-0.5 flex items-center gap-1 text-control text-fg">
            <MessageSquare className="h-3 w-3 text-fg-faint" aria-hidden />
            {entry.commentsFailed ? '-' : commentLabel(entry)}
          </dd>
        </div>
        <div className="min-w-0">
          <dt className={LABEL}>Updated</dt>
          <dd className="mt-0.5 text-control text-fg" title={exact(task.updated_at)}>{stamp(task.updated_at, now)}</dd>
        </div>
      </dl>
      {task.blocked_reason && <p className="mt-2 line-clamp-2 text-caption text-warning [overflow-wrap:anywhere]">Blocked: {task.blocked_reason}</p>}
      <span className="sr-only">{card.canonical}</span>
    </>
  )
}

let registered = false
/** Idempotent: gives torque.task its rich drawer and hovercard bodies. */
export function registerTaskRefs(): void {
  if (registered) return
  registered = true
  registerRefDetail('torque.task', TaskDrawerPane)
  registerRefHover('torque.task', TaskHoverMeta)
}

import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { ExternalLink } from 'lucide-react'
import { CopyableId, Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle, Skeleton } from '@hollis-labs/design-components'
import { StatusBadge } from '@planner/components/domain/status-badge'
import { useApi } from '@tachyon/torque-client'
import type { Comment, Task } from '@tachyon/torque-client'
import { HostLinkText } from '@tachyon/host-sdk'

const COMMENT_LIMIT = 5

function when(iso: string): string {
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? iso : d.toLocaleString()
}

/**
 * A quick view of a Torque task, opened from the planner board without leaving
 * it. The full page stays one click away.
 */
export function TorqueTaskDialog({ id, onClose }: { id: string; onClose: () => void }) {
  const api = useApi()
  const [task, setTask] = useState<Task | null>(null)
  const [comments, setComments] = useState<Comment[]>([])
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    const ctl = new AbortController()
    setTask(null)
    setComments([])
    setError(null)
    api.getTask(id, ctl.signal).then(setTask).catch((err: unknown) => {
      if (!ctl.signal.aborted) setError(err instanceof Error ? err.message : 'Failed to load the task')
    })
    // The newest comments, when the server will give them; the dialog works without.
    api.listComments('task', id, { limit: COMMENT_LIMIT, sort_by: 'created_at', sort_dir: 'desc' }, ctl.signal)
      .then((page) => setComments(page.items))
      .catch(() => undefined)
    return () => ctl.abort()
  }, [api, id])

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="dash-detail-dialog max-h-[88vh] overflow-auto" widthClassName="max-w-2xl" aria-describedby={undefined}>
        {error ? (
          <DialogHeader>
            <DialogTitle>{id}</DialogTitle>
            <DialogDescription>{error}</DialogDescription>
          </DialogHeader>
        ) : !task ? (
          <>
            <DialogHeader>
              <DialogTitle>{id}</DialogTitle>
            </DialogHeader>
            <Skeleton className="h-24 w-full" />
          </>
        ) : (
          <div className="flex flex-col gap-4">
            <DialogHeader>
              <div className="flex flex-wrap items-center gap-2 pr-8">
                <CopyableId id={task.id} />
                <StatusBadge status={task.status} />
                <span className="rounded bg-muted px-1.5 py-0.5 dash-label text-fg-muted">P{task.priority}</span>
                {task.tags.map((t) => (
                  <span key={t.slug} className="rounded bg-muted px-1.5 py-0.5 dash-label text-fg-muted">
                    {t.slug}
                  </span>
                ))}
              </div>
              <DialogTitle className="dash-body leading-6">{task.title}</DialogTitle>
            </DialogHeader>
            {task.description ? (
              <p className="max-h-72 overflow-auto whitespace-pre-wrap break-words dash-body leading-5"><HostLinkText surface="task-description" text={task.description} /></p>
            ) : (
              <p className="dash-body leading-5 text-fg-muted">No description.</p>
            )}
            {comments.length > 0 && (
              <section aria-label="Latest comments" className="flex flex-col gap-2">
                <h3 className="dash-body leading-5 font-semibold">Latest comments</h3>
                <ul className="flex flex-col gap-2">
                  {comments.map((c) => (
                    <li key={c.id} className="rounded-md border border-border p-3 dash-body leading-5">
                      <div className="mb-1 flex items-center gap-2 dash-label leading-4 text-fg-muted">
                        <span className="font-medium text-foreground">{c.author || 'unknown'}</span>
                        <span>{when(c.created_at)}</span>
                      </div>
                      <p className="line-clamp-6 whitespace-pre-wrap break-words"><HostLinkText surface="task-comments" text={c.content} /></p>
                    </li>
                  ))}
                </ul>
              </section>
            )}
            <div className="flex justify-end">
              <Link to={`/tasks/${encodeURIComponent(task.id)}`} className="inline-flex items-center gap-1 dash-body leading-5 text-primary hover:underline">
                Open the full task <ExternalLink className="h-3.5 w-3.5" />
              </Link>
            </div>
          </div>
        )}
      </DialogContent>
    </Dialog>
  )
}

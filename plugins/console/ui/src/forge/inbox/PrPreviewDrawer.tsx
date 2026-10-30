import { Button, Sheet, SheetContent, SheetDescription, SheetTitle } from '@hollis-labs/design-components'
import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import type { ForgeClient } from '../client'
import type { FileDiff, PullRequest } from '../types'
import { prWorkspaceHref } from './href'
import type { InboxItem } from './model'

const LABEL = 'font-mono dash-caption uppercase tracking-wide text-fg-faint'

function conclusion(c: PullRequest['checks'][number]) {
  if (c.status !== 'completed') return c.status === 'queued' ? 'queued' : 'running'
  return c.conclusion === 'success' ? 'passed' : (c.conclusion ?? 'done').replace(/_/g, ' ')
}

// Preview of one pull request from the inbox: enough to decide (assign, open the workspace)
// without leaving the list. Checks and files load on open; the row's own data shows at once.
export function PrPreviewDrawer({ item, forge, onClose, onAssign }: {
  item: InboxItem
  forge: ForgeClient
  onClose: () => void
  onAssign: (i: InboxItem) => void
}) {
  const { pr: summary } = item
  const [pr, setPr] = useState<PullRequest | null>(null)
  const [files, setFiles] = useState<FileDiff[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [attempt, setAttempt] = useState(0)
  useEffect(() => {
    let live = true
    setError(null)
    Promise.all([forge.getPr(summary.ref), forge.getDiff(summary.ref)]).then(
      ([p, f]) => { if (live) { setPr(p); setFiles(f) } },
      (e) => { if (live) setError(e instanceof Error ? e.message : 'Could not load this pull request') },
    )
    return () => { live = false }
  }, [forge, summary.ref, attempt])
  const label = `${summary.ref.repo.owner}/${summary.ref.repo.name}#${summary.ref.number}`
  const checks = pr?.checks ?? []
  const failing = checks.filter((c) => c.status === 'completed' && c.conclusion !== 'success' && c.conclusion !== 'skipped' && c.conclusion !== 'neutral')
  return (
    <Sheet open onOpenChange={(open) => { if (!open) onClose() }}>
      <SheetContent side="right" className="flex w-full flex-col gap-0 p-0 sm:max-w-lg">
        <header className="border-b border-border px-4 py-3">
          <SheetTitle className="font-sans text-base font-semibold normal-case tracking-normal text-fg">{summary.title}</SheetTitle>
          <SheetDescription className="mt-1 font-mono dash-support text-fg-muted">
            {label} · by {summary.author.login}{pr ? ` · ${pr.headBranch} → ${pr.baseBranch}` : ''}
          </SheetDescription>
        </header>
        <div className="min-h-0 flex-1 space-y-5 overflow-y-auto p-4">
          {error && (
            <div role="alert" className="flex items-center justify-between gap-2 border border-danger/40 px-3 py-2 dash-control text-danger">
              <span>Could not load this pull request: {error}</span>
              <Button variant="outline" size="sm" onClick={() => setAttempt((n) => n + 1)}>Retry</Button>
            </div>
          )}
          <section aria-label="Why it needs you">
            <h3 className={LABEL}>Needs you</h3>
            <p className="mt-1 dash-body text-fg">{item.categories.length ? item.categories.map((c) => c.replace(/_/g, ' ')).join(', ') : 'Nothing flagged'}</p>
          </section>
          {pr?.body && (
            <section aria-label="Description">
              <h3 className={LABEL}>Description</h3>
              <p className="mt-1 line-clamp-6 whitespace-pre-wrap dash-support text-fg-muted">{pr.body}</p>
            </section>
          )}
          <section aria-label="Checks">
            <h3 className={LABEL}>Checks</h3>
            {!pr && !error ? <p className="mt-1 dash-support text-fg-faint">Loading…</p> : checks.length === 0 ? <p className="mt-1 dash-support text-fg-faint">No checks reported</p> : (
              <>
                <p className="mt-1 dash-support text-fg-muted">{failing.length} failing, {checks.filter((c) => c.status !== 'completed').length} running, {checks.filter((c) => c.status === 'completed' && !failing.includes(c)).length} passed or skipped</p>
                <ul className="mt-1 divide-y divide-border">
                  {checks.map((c) => (
                    <li key={c.id} className="flex justify-between gap-2 py-1 dash-support">
                      <span className="truncate text-fg">{c.name}</span>
                      <span className={failing.includes(c) ? 'text-danger' : 'text-fg-muted'}>{conclusion(c)}</span>
                    </li>
                  ))}
                </ul>
              </>
            )}
          </section>
          <section aria-label="Changed files">
            <h3 className={LABEL}>Changed files{files ? ` (${files.length})` : ''}</h3>
            {!files && !error ? <p className="mt-1 dash-support text-fg-faint">Loading…</p> : (
              <ul className="mt-1 max-h-64 divide-y divide-border overflow-y-auto">
                {(files ?? []).map((f) => (
                  <li key={f.path} className="flex justify-between gap-2 py-1 font-mono dash-support">
                    <span className="truncate text-fg">{f.path}</span>
                    <span className="shrink-0 text-fg-muted">+{f.additions} −{f.deletions}</span>
                  </li>
                ))}
              </ul>
            )}
          </section>
        </div>
        <footer className="flex flex-wrap items-center gap-2 border-t border-border px-4 py-3">
          <Button render={<Link to={prWorkspaceHref(summary.ref)} />}>Open workspace</Button>
          <Button variant="outline" onClick={() => onAssign(item)}>{item.assignments.length ? 'Assign again' : 'Assign'}</Button>
        </footer>
      </SheetContent>
    </Sheet>
  )
}

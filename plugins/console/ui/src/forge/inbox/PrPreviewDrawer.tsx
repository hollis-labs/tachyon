import { Button, buttonVariants, OverflowMenu, Sheet, SheetContent, SheetDescription, SheetTitle, type OverflowAction } from '@hollis-labs/design-components'
import { ArrowUpRight, GitPullRequest } from 'lucide-react'
import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { ago } from '@console/lib/agent-activity'
import { Chip } from '../../components/chip'
import { InitialsAvatar } from '../../components/initials-avatar'
import { SegmentMeter } from '../../components/segment-meter'
import type { ForgeClient } from '../client'
import type { CheckRun, FileDiff, ForgeComment, PullRequest } from '../types'
import { prWorkspaceHref } from './href'
import { CATEGORY_LABEL, type InboxCategory, type InboxItem } from './model'

const LABEL = 'font-mono dash-caption uppercase tracking-label text-fg-muted'
const FILE_STATUS = { added: 'A', removed: 'D', renamed: 'R', modified: 'M' } as Record<string, string>
const DANGER: InboxCategory[] = ['failing_checks', 'conflicts', 'changes_requested']
const SHOWN_CHECKS = 5
const SHOWN_FILES = 3

const failed = (c: CheckRun) => c.status === 'completed' && c.conclusion !== 'success' && c.conclusion !== 'skipped' && c.conclusion !== 'neutral'

function duration(c: CheckRun) {
  if (!c.startedAt || !c.completedAt) return ''
  const s = Math.max(0, Math.round((Date.parse(c.completedAt) - Date.parse(c.startedAt)) / 1000))
  return s >= 60 ? `${Math.floor(s / 60)}m ${String(s % 60).padStart(2, '0')}s` : `${s}s`
}

function outcome(c: CheckRun) {
  if (c.status !== 'completed') return c.status === 'queued' ? 'queued' : 'running'
  if (c.conclusion === 'success') return ''
  return (c.conclusion ?? 'done').replace(/_/g, ' ').replace('failure', 'failed')
}

const Section = ({ title, children }: { title: string; children: React.ReactNode }) => (
  <section aria-label={title}>
    <h3 className={`mb-2 ${LABEL}`}>{title}</h3>
    {children}
  </section>
)

// Preview of one pull request from the inbox: enough to decide (assign, open the workspace)
// without leaving the list. Checks, conversation and files load on open; the row's own data shows at once.
export function PrPreviewDrawer({ item, forge, actions = [], onClose, onAssign, onAskReview }: {
  item: InboxItem
  forge: ForgeClient
  /** Triage actions for the overflow menu. */
  actions?: OverflowAction[]
  onClose: () => void
  onAssign: (i: InboxItem) => void
  onAskReview: (i: InboxItem) => void
}) {
  const { pr: summary } = item
  const [pr, setPr] = useState<PullRequest | null>(null)
  const [files, setFiles] = useState<FileDiff[] | null>(null)
  const [comments, setComments] = useState<ForgeComment[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [attempt, setAttempt] = useState(0)
  useEffect(() => {
    let live = true
    setError(null)
    Promise.all([forge.getPr(summary.ref), forge.getDiff(summary.ref), forge.listComments(summary.ref)]).then(
      ([p, f, c]) => { if (live) { setPr(p); setFiles(f); setComments(c) } },
      (e) => { if (live) setError(e instanceof Error ? e.message : 'Could not load this pull request') },
    )
    return () => { live = false }
  }, [forge, summary.ref, attempt])
  const label = `${summary.ref.repo.name}#${summary.ref.number}`
  const checks = pr?.checks ?? []
  const bad = checks.filter(failed)
  const passed = checks.filter((c) => c.status === 'completed' && !failed(c)).length
  const running = checks.length - bad.length - passed
  const listed = [...bad, ...checks.filter((c) => !failed(c))].slice(0, bad.length > SHOWN_CHECKS ? bad.length : SHOWN_CHECKS)
  const latest = [...(comments ?? [])].sort((a, b) => b.createdAt.localeCompare(a.createdAt)).slice(0, 2)
  const reviews = pr?.reviews ?? []
  const changed = files ?? []
  const now = Date.now()
  return (
    <Sheet open onOpenChange={(open) => { if (!open) onClose() }}>
      <SheetContent side="right" className="flex !w-full max-w-none flex-col gap-0 p-0 sm:!w-full sm:max-w-lg">
        <header className="flex flex-wrap items-center gap-2 py-3 pl-4 pr-12">
          <GitPullRequest aria-hidden className="size-4 text-fg-muted" />
          <span className="font-mono dash-body text-fg" title={`${summary.ref.repo.owner}/${summary.ref.repo.name}#${summary.ref.number}`}>{label}</span>
          {item.categories.filter((c) => DANGER.includes(c)).map((c) => <Chip key={c} tone="danger">{CATEGORY_LABEL[c]}</Chip>)}
          {summary.draft && <Chip>Draft</Chip>}
        </header>
        <div className="min-h-0 flex-1 space-y-5 overflow-y-auto px-4 pb-4">
          <div>
            <SheetTitle className="font-sans text-lg font-semibold normal-case leading-snug tracking-normal text-fg">{summary.title}</SheetTitle>
            <SheetDescription className="mt-2 dash-support text-fg-muted">
              {summary.author.login} · updated {ago(Date.parse(summary.updatedAt), now)}
              {pr ? ` · ` : ''}{pr && <span className="font-mono">{pr.headBranch} → {pr.baseBranch}</span>}
              {summary.additions !== undefined && <> · <span className="font-mono">+{summary.additions} −{summary.deletions ?? 0}</span>{summary.changedFiles !== undefined && ` in ${summary.changedFiles} ${summary.changedFiles === 1 ? 'file' : 'files'}`}</>}
            </SheetDescription>
          </div>
          {error && (
            <div role="alert" className="flex items-center justify-between gap-2 border border-danger/40 px-3 py-2 dash-control text-danger">
              <span>Could not load this pull request: {error}</span>
              <Button variant="outline" size="sm" onClick={() => setAttempt((n) => n + 1)}>Retry</Button>
            </div>
          )}
          <Section title="Checks">
            {!pr && !error ? <p className="dash-support text-fg-faint">Loading…</p> : checks.length === 0 ? <p className="dash-support text-fg-faint">No checks reported</p> : (
              <>
                <div className="mb-3 flex items-center gap-3">
                  <SegmentMeter label="Checks" className="flex-1" segments={[{ count: passed, tone: 'ok', label: 'passed' }, { count: bad.length, tone: 'bad', label: 'failed' }, { count: running, tone: 'pending', label: 'running' }]} />
                  <span className="font-mono dash-caption text-fg-muted">{[`${passed} passed`, bad.length ? `${bad.length} failed` : '', running ? `${running} running` : ''].filter(Boolean).join(' · ')}</span>
                </div>
                <ul className="divide-y divide-border rounded border border-border">
                  {listed.map((c) => (
                    <li key={c.id} className="flex items-center gap-2 px-3 py-2 dash-support">
                      <span aria-hidden className={`w-3 text-center ${failed(c) ? 'text-danger' : 'text-fg-muted'}`}>{failed(c) ? '✗' : c.status === 'completed' ? '✓' : '·'}</span>
                      <span className="min-w-0 truncate font-medium text-fg">{c.name}</span>
                      <span className="shrink-0 text-fg-muted">{[outcome(c), duration(c)].filter(Boolean).join(' · ')}</span>
                      {c.url && <a href={c.url} target="_blank" rel="noreferrer" className="ml-auto inline-flex shrink-0 items-center gap-0.5 text-fg-muted hover:text-fg">Logs <ArrowUpRight className="size-3" aria-hidden /></a>}
                    </li>
                  ))}
                </ul>
                {checks.length > listed.length && <p className="mt-1 dash-support text-fg-faint">+ {checks.length - listed.length} more checks</p>}
              </>
            )}
          </Section>
          {pr && (
            <Section title="Reviews">
              {reviews.length === 0 ? <p className="dash-support text-fg-muted">No reviews yet.{(comments?.length ?? 0) > 0 ? ' Comment only so far.' : ''}</p> : (
                <ul className="space-y-1 dash-support">
                  {reviews.map((r) => <li key={r.id} className="flex gap-2"><span className="font-medium text-fg">{r.author.login}</span><span className={r.state === 'changes_requested' ? 'text-danger' : 'text-fg-muted'}>{r.state.replace(/_/g, ' ')}</span></li>)}
                </ul>
              )}
            </Section>
          )}
          {latest.length > 0 && (
            <Section title="Latest conversation">
              <ul className="divide-y divide-border rounded border border-border">
                {latest.map((c) => (
                  <li key={c.id} className="flex gap-3 px-3 py-2.5">
                    <InitialsAvatar name={c.author.login} className="mt-0.5" />
                    <div className="min-w-0">
                      <p className="dash-support text-fg-muted"><span className="font-medium text-fg">{c.author.login}</span> · {ago(Date.parse(c.createdAt), now)}</p>
                      <p className="line-clamp-2 dash-support text-fg-muted">{c.body}</p>
                    </div>
                  </li>
                ))}
              </ul>
            </Section>
          )}
          <Section title="Files changed">
            {!files && !error ? <p className="dash-support text-fg-faint">Loading…</p> : (
              <>
                <ul className="divide-y divide-border rounded border border-border">
                  {changed.slice(0, SHOWN_FILES).map((f) => (
                    <li key={f.path} className="flex items-center gap-3 px-3 py-2 font-mono dash-support">
                      <span aria-hidden className="text-fg-muted">{FILE_STATUS[f.status] ?? 'M'}</span>
                      <span className="min-w-0 flex-1 truncate font-medium text-fg" title={f.path}>{f.path}</span>
                      <span className="shrink-0 text-fg-muted">+{f.additions}</span>
                    </li>
                  ))}
                  {changed.length > SHOWN_FILES && <li className="px-3 py-2 text-right dash-support text-fg-muted">+ {changed.length - SHOWN_FILES} more {changed.length - SHOWN_FILES === 1 ? 'file' : 'files'}</li>}
                </ul>
              </>
            )}
          </Section>
        </div>
        <footer className="flex items-center gap-2 border-t border-border px-4 py-3">
          <Button size="sm" onClick={() => onAssign(item)}>{item.assignments.length ? 'Assign again' : 'Assign agent'}</Button>
          <Button variant="outline" size="sm" onClick={() => onAskReview(item)}>Ask for review</Button>
          <Link to={prWorkspaceHref(summary.ref)} data-slot="button" className={buttonVariants({ variant: 'outline', size: 'sm' })}>Open workspace</Link>
          <span className="ml-auto"><OverflowMenu ariaLabel="More actions" actions={actions} /></span>
        </footer>
      </SheetContent>
    </Sheet>
  )
}

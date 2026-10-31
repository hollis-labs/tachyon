import { useContext, useEffect, useMemo, useState, type ReactNode } from 'react'
import { ArrowUpRight, ExternalLink, RefreshCw } from 'lucide-react'
import { Button, buttonVariants, DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from '@hollis-labs/design-components'
import { ago } from '@console/lib/agent-activity'
import { SeamsContext } from '../../events/context'
import { Chip } from '../../components/chip'
import { InitialsAvatar } from '../../components/initials-avatar'
import { PillTabs } from '../../components/pill-tabs'
import { Segmented } from '../../components/segmented'
import { SegmentMeter } from '../../components/segment-meter'
import { AssignDialog } from '../inbox/AssignDialog'
import { ASSIGN_TASKS, type AssignTask } from '../inbox/assign'
import { useNow } from '../inbox/hooks'
import { toSummary } from './to-summary'
import { StepUpHost } from '../../agents/StepUpDialog'
import { Linkified } from '../../autolink/Linkified'
import type { StatusTone } from '../../refs'
import { createActionBus, MockIdentityConfirm, type ActionBus, type IdentityConfirm } from '../../seams'
import type { ForgeClient } from '../client'
import { checksText, checksTone, reviewText, sizeText, summarizeChecks } from '../refs/entity'
import { getForgeClient } from '../refs/client'
import { hostLabel, prUrl } from '../refs/parse'
import type { CheckRun, ForgeComment, ForgeUser, PrRef, PullRequest, Review } from '../types'
import { registerWorkspaceActions } from './actions'
import { Author, Badge, CommentItem, Composer, LABEL, ThreadView } from './Comments'
import { buildThreadIndex, type Thread } from './diff-utils'
import { FileDiffView, type DiffMode } from './DiffView'
import { FileTree } from './FileTree'
import { MergePanel } from './MergePanel'
import { ReviewPanel } from './ReviewPanel'
import { diffLineCount, fileAnchorId, LARGE_FILE_LINES } from './diff-utils'
import { useWorkspace } from './useWorkspace'

export interface PrWorkspaceProps {
  prRef: PrRef
  /** Defaults to the installed ForgeClient (mock; `?forge=http` selects HTTP). */
  client?: ForgeClient
  bus?: ActionBus
  identity?: IdentityConfirm
  /** Hide every write affordance. */
  readOnly?: boolean
  /** File to scroll to once loaded. */
  initialFile?: string
  initialMode?: DiffMode
  className?: string
  /** Left end of the top bar (the page's back link). */
  leading?: ReactNode
}

type Tab = 'files' | 'conversation' | 'checks'

const ACTOR: ForgeUser = { login: 'you' }

function checkTone(c: CheckRun): StatusTone {
  if (c.status !== 'completed') return 'warning'
  if (c.conclusion === 'success') return 'success'
  if (c.conclusion === 'neutral' || c.conclusion === 'skipped') return 'neutral'
  return 'danger'
}

function mergeNote(pr: PullRequest): { tone: StatusTone; text: string } {
  if (pr.state === 'merged') return { tone: 'info', text: pr.mergedAt ? `Merged ${new Date(pr.mergedAt).toISOString().slice(0, 10)}` : 'Merged' }
  if (pr.state === 'closed') return { tone: 'danger', text: 'Closed without merging' }
  const checks = summarizeChecks(pr.checks)
  const changes = pr.reviews.some((r) => r.state === 'changes_requested')
  if (pr.mergeable === 'conflicting') return { tone: 'danger', text: 'Has merge conflicts' }
  if (pr.draft) return { tone: 'neutral', text: 'Draft: not ready to merge' }
  if (checks.failed) return { tone: 'danger', text: 'Checks failing' }
  if (changes) return { tone: 'danger', text: 'Changes requested' }
  if (checks.pending) return { tone: 'warning', text: 'Waiting on checks' }
  if (pr.mergeable === 'blocked') return { tone: 'warning', text: 'Blocked by branch rules' }
  if (pr.mergeable === 'unknown') return { tone: 'neutral', text: 'Merge state not computed yet' }
  return { tone: 'success', text: 'Ready to merge' }
}

function useSeams(bus?: ActionBus, identity?: IdentityConfirm): { bus: ActionBus; identity: IdentityConfirm } {
  const seams = useContext(SeamsContext)
  return useMemo(() => {
    if (bus && identity) return { bus, identity }
    if (seams) return { bus: bus ?? seams.actionBus, identity: identity ?? seams.identity }
    // No Console provider: a local bus with a MANUAL mock step-up, so the confirm dialog always shows.
    const id = identity ?? new MockIdentityConfirm({ mode: 'manual' })
    return { bus: bus ?? createActionBus({ identity: id }), identity: id }
  }, [bus, identity, seams])
}

/**
 * PR workspace: header (state, checks, reviews, merge state), file tree,
 * unified/split diff with inline threads, conversation, checks, and review
 * submission. Reads and writes go through the ForgeClient; writes dispatch on
 * the ActionBus (reviews behind IdentityConfirm).
 */
export function PrWorkspace({ prRef, client: clientProp, bus: busProp, identity: identityProp, readOnly: readOnlyProp, initialFile, initialMode = 'unified', className, leading }: PrWorkspaceProps) {
  const client = useMemo(() => clientProp ?? getForgeClient(), [clientProp])
  const { bus, identity } = useSeams(busProp, identityProp)
  useEffect(() => registerWorkspaceActions(bus, client), [bus, client])
  const ws = useWorkspace(client, bus, prRef, ACTOR)

  const [tab, setTab] = useState<Tab>('files')
  const [mode, setMode] = useState<DiffMode>(initialMode)
  const validateQueued = useMemo(() => {
    const headSha = ws.load.status === 'ready' ? ws.load.data.pr.headSha : undefined
    if (!client.validateReview || !headSha) return undefined
    return (pending: typeof ws.pending) => client.validateReview!(prRef, { headSha, comments: pending.map((c) => ({ body: c.body, path: c.path, line: c.line, side: c.side })) })
  }, [client, prRef, ws.load])
  const [reviewing, setReviewing] = useState(false)
  const [asking, setAsking] = useState<AssignTask | null>(null)
  const [mergeBusy, setMergeBusy] = useState(false)
  const [merging, setMerging] = useState(false)
  const [mergeSuccess, setMergeSuccess] = useState('')
  useEffect(() => { setMerging(false); setMergeSuccess(''); setReviewing(false) }, [prRef.repo.host, prRef.repo.owner, prRef.repo.name, prRef.number])
  const [closedFiles, setClosedFiles] = useState<Set<string> | null>(null)
  const [openedLarge, setOpenedLarge] = useState<Set<string>>(new Set())
  const [active, setActive] = useState<string | undefined>(initialFile)
  const now = useNow()

  const ready = ws.load.status === 'ready' ? ws.load.data : null
  const index = useMemo(() => (ready ? buildThreadIndex(ready.comments, ready.files, ready.pr.headSha) : null), [ready])
  const threadsByPath = useMemo(() => {
    const m = new Map<string, number>()
    for (const t of index?.all ?? []) m.set(t.path, (m.get(t.path) ?? 0) + 1)
    return m
  }, [index])

  const isLarge = (path: string) => {
    const f = ready?.files.find((x) => x.path === path)
    return !!f && diffLineCount(f) >= LARGE_FILE_LINES
  }
  const isOpen = (path: string) => (isLarge(path) ? openedLarge.has(path) || path === initialFile : !(closedFiles?.has(path) ?? false))
  const toggle = (path: string) => {
    if (isLarge(path)) setOpenedLarge((s) => toggled(s, path))
    else setClosedFiles((s) => toggled(s ?? new Set(), path))
  }

  const select = (path: string) => {
    setActive(path)
    setTab('files')
    if (isLarge(path)) setOpenedLarge((s) => new Set(s).add(path))
    else setClosedFiles((s) => (s?.has(path) ? toggled(s, path) : s))
    requestAnimationFrame(() => document.getElementById(fileAnchorId(path))?.scrollIntoView?.({ block: 'start' }))
  }

  const loaded = ready !== null
  useEffect(() => {
    if (!initialFile || !loaded) return
    const id = requestAnimationFrame(() => document.getElementById(fileAnchorId(initialFile))?.scrollIntoView?.({ block: 'start' }))
    return () => cancelAnimationFrame(id)
  }, [initialFile, loaded])

  if (ws.load.status === 'loading') {
    return (
      <div className={`p-6 ${className ?? ''}`} role="status" aria-label="Loading pull request" data-testid="pr-workspace-loading">
        <div className="space-y-2" aria-hidden>
          <div className="h-3 w-1/3 animate-pulse bg-surface" />
          <div className="h-2 w-1/4 animate-pulse bg-surface" />
          <div className="mt-6 h-40 animate-pulse bg-surface" />
        </div>
      </div>
    )
  }
  if (ws.load.status === 'error' || !ready || !index) {
    const err = ws.load.status === 'error' ? ws.load : { message: 'Unknown error', code: undefined }
    return (
      <div className={`p-6 ${className ?? ''}`} role="alert" data-testid="pr-workspace-error">
        <h1 className="font-mono text-xs uppercase tracking-label text-danger">{err.code === 'not_found' ? 'Pull request not found' : 'Could not load pull request'}</h1>
        <p className="mt-2 text-control text-fg-muted">{err.message}</p>
        <Button variant="outline" size="sm" className="mt-4" onClick={ws.reload}>Retry</Button>
      </div>
    )
  }

  const { pr, files } = ready
  const readOnly = !!readOnlyProp || pr.state === 'merged' || pr.state === 'closed'
  const checks = summarizeChecks(pr.checks)
  const merge = mergeNote(pr)
  const href = pr.url ?? prUrl(prRef)
  const convo = timeline(ready.comments.filter((c) => c.kind === 'issue'), pr.reviews, index.all.filter((t) => !index.orphaned.includes(t)))
  const postingIssue = ws.posting.filter((p) => p.kind === 'issue')
  const allOpen = files.every((f) => isOpen(f.path) || isLarge(f.path))
  const failing = pr.checks.filter((c) => checkTone(c) === 'danger')
  const running = pr.checks.filter((c) => checkTone(c) !== 'danger' && c.status !== 'completed')
  const passing = pr.checks.filter((c) => checkTone(c) !== 'danger' && c.status === 'completed')
  const totalAdds = files.reduce((n, f) => n + f.additions, 0)
  const totalDels = files.reduce((n, f) => n + f.deletions, 0)

  return (
    <div className={`flex h-full min-h-120 flex-col bg-bg ${className ?? ''}`} data-testid="pr-workspace">
      <div className="flex shrink-0 items-center justify-between gap-3 border-b border-border px-4 py-2">
        <span className="min-w-0 text-xs text-fg-muted">{leading}</span>
        <span className="flex items-center gap-2 text-xs text-fg-muted">
          <span className="max-md:hidden">Updated {ago(ws.loadedAt, now)}</span>
          <Button variant="outline" size="icon-sm" onClick={ws.reload} disabled={mergeBusy} aria-label="Refresh pull request"><RefreshCw className="size-3.5" /></Button>
        </span>
      </div>
      <header className="shrink-0 border-b border-border px-4 pb-0 pt-4">
        <div className="flex flex-wrap items-start gap-x-4 gap-y-2">
          <h1 className="min-w-0 flex-1 basis-80 font-sans text-xl font-semibold normal-case leading-snug tracking-normal text-fg [overflow-wrap:anywhere]">
            {pr.title} <span className="font-mono font-normal text-fg-faint">#{pr.ref.number}</span>
          </h1>
          <span className="flex flex-wrap items-center gap-2">
            {!readOnly && (
              <DropdownMenu>
                <DropdownMenuTrigger render={<Button variant="outline" size="sm" />}>Ask an agent ▾</DropdownMenuTrigger>
                <DropdownMenuContent align="end" aria-label="Ask an agent" className="w-48">
                  {ASSIGN_TASKS.map((t) => <DropdownMenuItem key={t.id} onClick={() => setAsking(t.id)}>{t.label}</DropdownMenuItem>)}
                </DropdownMenuContent>
              </DropdownMenu>
            )}
            {href && <a href={href} target="_blank" rel="noreferrer" data-slot="button" className={buttonVariants({ variant: 'outline', size: 'sm' })}>{hostLabel(prRef.repo.host)} <ExternalLink className="size-3" /></a>}
            {!readOnly && (
              <Button variant="outline" size="sm" disabled={mergeBusy} onClick={() => { setMerging(false); setReviewing((r) => !r) }} aria-expanded={reviewing}>
                Review{ws.pending.length > 0 && <>{' '}<span className="text-caption text-fg-muted">{ws.pending.length} queued</span></>} ▾
              </Button>
            )}
            {!readOnly && <Button size="sm" disabled={mergeBusy} onClick={() => { setReviewing(false); setMerging((m) => !m) }} aria-expanded={merging}>Merge</Button>}
          </span>
        </div>
        <p className="mt-2 flex flex-wrap items-center gap-x-4 gap-y-1 text-xs text-fg-muted">
          <span className="font-mono">{prRef.repo.owner}/{prRef.repo.name}</span>
          <Author user={pr.author} />
          <span className="font-mono [overflow-wrap:anywhere]">{pr.headBranch} {'→'} {pr.baseBranch}</span>
          {sizeText(pr) && <span className="font-mono">{sizeText(pr)}</span>}
        </p>
        <p className="mt-2 flex flex-wrap items-center gap-1.5">
          {pr.draft && pr.state === 'open' && <Chip>Draft</Chip>}
          {pr.state !== 'open' && <Chip>{pr.state}</Chip>}
          <Chip tone={merge.tone === 'danger' ? 'danger' : 'neutral'}>{merge.text}</Chip>
          <Chip tone={checksTone(checks) === 'danger' ? 'danger' : 'neutral'}>{checksText(checks)}</Chip>
          <Chip>{reviewText(pr.reviews)}</Chip>
        </p>
        <PillTabs
          label="Workspace sections"
          className="mt-3 pb-2"
          value={tab}
          onChange={(v) => setTab(v as Tab)}
          items={[
            { value: 'files', label: 'Files changed', count: files.length },
            { value: 'conversation', label: 'Conversation', count: ready.comments.length },
            { value: 'checks', label: 'Checks', count: pr.checks.length },
          ]}
        />
      </header>

      {mergeSuccess && <p role="status" className="border-b border-border px-4 py-3 text-control text-fg">{mergeSuccess}</p>}
      {merging && !readOnly && <MergePanel client={client} pr={pr} fileCount={files.length} onBusyChange={setMergeBusy} onClose={() => setMerging(false)} onSubmit={async (input) => {
        const result = await ws.mergePr(input)
        if (result.ok) setMergeSuccess(`Pull request #${pr.ref.number} merged into ${pr.baseBranch}.`)
        return result
      }} />}

      {reviewing && !readOnly && (
        <ReviewPanel validate={validateQueued} prLabel={`${prRef.repo.name}#${prRef.number}`} pending={ws.pending} onUnqueue={ws.unqueueComment} onClose={() => setReviewing(false)} onSubmit={({ verdict, body }) => ws.submitReview({ verdict, body })} />
      )}
      {asking && <AssignDialog pr={toSummary(pr)} existing={[]} initialTask={asking} onClose={() => setAsking(null)} />}

      {tab === 'files' && (
        <div className="flex min-h-0 flex-1">
          <aside className="hidden w-72 shrink-0 flex-col border-r border-border md:flex">
            <FileTree files={files} active={active} threads={threadsByPath} onSelect={select} />
          </aside>
          <div className="min-w-0 flex-1 overflow-auto" data-testid="forge-diff-scroll">
            <div className="flex flex-wrap items-center gap-3 border-b border-border px-3 py-2">
              <Segmented bare label="Diff layout" value={mode} onChange={(m) => setMode(m)} items={[{ value: 'unified', label: 'Unified' }, { value: 'split', label: 'Split' }]} />
              <Button variant="outline" size="sm" onClick={() => (allOpen ? setClosedFiles(new Set(files.map((f) => f.path))) : setClosedFiles(new Set()))}>
                {allOpen ? 'Collapse all' : 'Expand all'}
              </Button>
              <span className="ml-auto text-label text-fg-muted">
                {files.length} file{files.length === 1 ? '' : 's'} · <span className="font-mono">+{totalAdds} {'−'}{totalDels}</span>
                {ws.pending.length > 0 && <> · <span className="font-medium text-fg">{ws.pending.length} queued comment{ws.pending.length === 1 ? '' : 's'}</span></>}
              </span>
              {readOnly && <Badge>read only</Badge>}
            </div>
            {files.length === 0 && <p className="px-4 py-6 text-control text-fg-faint">No file changes in this pull request.</p>}
            {files.map((f) => (
              <FileDiffView
                key={f.path}
                file={f}
                mode={mode}
                open={isOpen(f.path)}
                onToggle={() => toggle(f.path)}
                index={index}
                posting={ws.posting}
                pending={ws.pending.filter((p) => p.path === f.path)}
                readOnly={readOnly}
                forgeUrl={href}
                postComment={ws.postComment}
                queueComment={ws.queueComment}
                unqueueComment={ws.unqueueComment}
              />
            ))}
          </div>
        </div>
      )}

      {tab === 'conversation' && (
        <div className="min-h-0 flex-1 overflow-auto px-4 py-6" data-testid="forge-conversation">
          <div className="mx-auto max-w-3xl">
            <ol className="relative space-y-5 border-l border-border pl-6">
              <li className="relative">
                <span className="absolute -left-9 top-0"><InitialsAvatar name={pr.author.name ?? pr.author.login} className="size-6 bg-bg" /></span>
                <p className="text-control text-fg-muted"><Author user={pr.author} /> opened this · {ago(Date.parse(pr.createdAt), now)}</p>
                <div className="mt-1 text-control leading-relaxed text-fg [overflow-wrap:anywhere]">
                  {pr.body?.trim() ? <Linkified text={pr.body} preserveWhitespace /> : <span className="text-fg-faint">No description provided.</span>}
                </div>
              </li>
              {convo.map((e) => (
                <li key={e.key} className="relative">
                  <span className="absolute -left-9 top-0"><InitialsAvatar name={e.author.name ?? e.author.login} className="size-6 bg-bg" /></span>
                  <p className="text-control text-fg-muted">
                    <Author user={e.author} /> · {ago(Date.parse(e.at), now)}{e.where ? ` · ${e.where}` : ''}
                    {e.type === 'review' && <span className="ml-2 inline-flex align-middle"><Chip tone={e.review.state === 'changes_requested' ? 'danger' : 'neutral'}>{e.review.state.replace('_', ' ')}</Chip></span>}
                    {e.type === 'thread' && e.resolved && <span className="ml-2 inline-flex align-middle"><Chip>Resolved</Chip></span>}
                  </p>
                  {e.body && <div className="mt-1 text-control leading-relaxed text-fg [overflow-wrap:anywhere]"><Linkified text={e.body} preserveWhitespace /></div>}
                  {e.type === 'thread' && !e.resolved && !readOnly && (
                    <div className="mt-2"><Button variant="outline" size="xs" onClick={() => select(e.path)}>Open in files</Button></div>
                  )}
                </li>
              ))}
              {postingIssue.map((c) => <li key={c.id} className="relative"><CommentItem c={c} posting /></li>)}
              {convo.length + postingIssue.length === 0 && <li className="text-xs text-fg-faint">No comments yet.</li>}
            </ol>
            {index.orphaned.length > 0 && (
              <div className="mt-6">
                <h2 className={`${LABEL} mb-2`}>Comments on files no longer in the diff · {index.orphaned.length}</h2>
                {index.orphaned.map((t) => (
                  <ThreadView key={t.id} thread={t} posting={ws.posting} readOnly={readOnly} showPath onReply={(id, body) => ws.postComment({ body, path: t.path, line: t.root.line, side: t.root.side, inReplyTo: id })} />
                ))}
              </div>
            )}
            {!readOnly && (
              <div className="mt-6">
                <Composer placeholder="Add a comment…" submitLabel="Comment" onSubmit={(body) => ws.postComment({ body })} />
              </div>
            )}
          </div>
        </div>
      )}

      {tab === 'checks' && (
        <div className="min-h-0 flex-1 overflow-auto px-4 py-6" data-testid="forge-checks">
          <div className="mx-auto max-w-3xl">
            {pr.checks.length === 0 ? (
              <p className="text-control text-fg-faint">No checks reported for this pull request.</p>
            ) : (
              <>
                <div className="flex items-center gap-3">
                  <SegmentMeter label="Checks" className="flex-1" segments={[{ count: checks.passed, tone: 'ok', label: 'passed' }, { count: checks.failed, tone: 'bad', label: 'failed' }, { count: checks.pending, tone: 'pending', label: 'pending' }]} />
                  <span className="font-mono text-caption text-fg-muted">{checks.passed} passed · {checks.failed} failed · {checks.pending} pending</span>
                </div>
                {[['Failing', failing], ['Pending', running], ['Passed', passing]].map(([title, list]) => (list as CheckRun[]).length > 0 && (
                  <section key={title as string} className="mt-5">
                    <h2 className={`${LABEL} mb-2`}>{title as string}</h2>
                    <ul className="divide-y divide-border rounded border border-border">
                      {(list as CheckRun[]).map((c) => (
                        <li key={c.id} className="flex items-center gap-3 px-3 py-2 text-control">
                          <span aria-hidden className={`w-3 text-center ${checkTone(c) === 'danger' ? 'text-danger' : 'text-fg-muted'}`}>{checkTone(c) === 'danger' ? '✗' : c.status === 'completed' ? '✓' : '·'}</span>
                          <span className="min-w-0 truncate font-medium text-fg">{c.name}</span>
                          <span className="shrink-0 text-xs text-fg-muted">{[checkTone(c) === 'danger' ? (c.conclusion ?? 'failed').replace('failure', 'failed').replace('_', ' ') : c.status !== 'completed' ? c.status.replace('_', ' ') : '', checkDuration(c)].filter(Boolean).join(' · ')}</span>
                          {c.url && <a href={c.url} target="_blank" rel="noreferrer" aria-label={`Open ${c.name} on the forge`} className="ml-auto inline-flex shrink-0 items-center gap-0.5 text-xs text-fg-muted hover:text-fg">Logs <ArrowUpRight className="size-3" aria-hidden /></a>}
                        </li>
                      ))}
                    </ul>
                  </section>
                ))}
                {failing.length > 0 && !readOnly && <Button size="sm" className="mt-4" onClick={() => setAsking('fix_checks')}>Ask an agent to fix checks ▸</Button>}
              </>
            )}
          </div>
        </div>
      )}
      <StepUpHost identity={identity} />
    </div>
  )
}

function toggled<T>(s: Set<T>, v: T): Set<T> {
  const n = new Set(s)
  if (!n.delete(v)) n.add(v)
  return n
}

function checkDuration(c: CheckRun) {
  if (!c.startedAt || !c.completedAt) return ''
  const sec = Math.max(0, Math.round((Date.parse(c.completedAt) - Date.parse(c.startedAt)) / 1000))
  return sec >= 60 ? `${Math.floor(sec / 60)}m ${String(sec % 60).padStart(2, '0')}s` : `${sec}s`
}

interface TimelineEntry {
  key: string
  type: 'comment' | 'review' | 'thread'
  at: string
  author: ForgeUser
  body?: string
  where?: string
  path: string
  resolved?: boolean
  review: Review
}

function timeline(issue: ForgeComment[], reviews: Review[], threads: Thread[]): TimelineEntry[] {
  const none = {} as Review
  return [
    ...issue.map((c): TimelineEntry => ({ key: c.id, type: 'comment', at: c.createdAt, author: c.author, body: c.body, path: '', review: none })),
    ...reviews.filter((r) => r.state !== 'pending').map((r): TimelineEntry => ({ key: r.id, type: 'review', at: r.submittedAt ?? '', author: r.author, body: r.body, path: '', review: r })),
    ...threads.map((t): TimelineEntry => ({ key: t.id, type: 'thread', at: t.root.createdAt, author: t.root.author, body: t.root.body, path: t.path, where: t.root.line != null ? `on line ${t.root.line}` : `on ${t.path}`, resolved: t.resolved, review: none })),
  ].sort((a, b) => a.at.localeCompare(b.at))
}

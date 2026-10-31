import { Button, Checkbox, EmptyState, OverflowMenu, type OverflowAction, SearchInput, DropdownMenu, DropdownMenuTrigger, DropdownMenuContent, DropdownMenuItem } from '@hollis-labs/design-components'
import { PageHeader } from '@hollis-labs/kit-dashboard'
import { GitPullRequest, RefreshCw } from 'lucide-react'
import { useEffect, useMemo, useState } from 'react'
import { Chip } from '../../components/chip'
import { PillTabs } from '../../components/pill-tabs'
import { InitialsAvatar } from '../../components/initials-avatar'
import { SegmentMeter } from '../../components/segment-meter'
import { ago } from '@console/lib/agent-activity'
import { useMessaging } from '../../messaging/context'
import { useConsoleSeams } from '../../events/hooks'
import type { ForgeClient } from '../client'
import { getForgeClient } from '../refs/client'
import { repoKey } from '../types'
import { registerInboxActions, type InboxActionDeps } from './actions'
import { AssignDialog } from './AssignDialog'
import { PrPreviewDrawer } from './PrPreviewDrawer'
import { useItemStates, useNow, usePendingPrs } from './hooks'
import {
  buildItems,
  CATEGORY_LABEL,
  CATEGORY_ORDER,
  countByCategory,
  countByView,
  DEFAULT_FILTER,
  DEFAULT_STALE_MS,
  filterItems,
  groupItems,
  repoOptions,
  SNOOZE_PRESETS,
  type GroupBy,
  type InboxCategory,
  type InboxFilter,
  type InboxItem,
  type TriageView,
} from './model'
import { ASSIGN_TASKS, type AssignTask } from './assign'
import { useAgentsClient } from '../../agents/context'

const DANGER_CATEGORIES: InboxCategory[] = ['failing_checks', 'conflicts', 'changes_requested']

const VIEWS: { id: Exclude<TriageView, 'all'>; label: string }[] = [
  { id: 'active', label: 'Active' },
  { id: 'snoozed', label: 'Snoozed' },
  { id: 'dismissed', label: 'Dismissed' },
  { id: 'settled', label: 'Resolved' },
]


const COLLAPSE_AFTER = 4
const MORE_NOUN: Record<InboxCategory, string> = {
  failing_checks: 'with failing checks',
  changes_requested: 'with requested changes',
  conflicts: 'with conflicts',
  needs_review: 'needing review',
  stale: 'gone stale',
  unassigned: 'unassigned',
}

function ChecksMeter({ item }: { item: InboxItem }) {
  const c = item.pr.checksSummary
  const r = item.pr.reviewSummary
  const total = c ? c.passed + c.failed + c.pending : 0
  return (
    <div className="flex w-28 shrink-0 flex-col items-end gap-1 max-md:hidden">
      {c && total > 0 && (
        <>
          <SegmentMeter label="Checks" className="w-full" segments={[{ count: c.passed, tone: 'ok', label: 'passed' }, { count: c.failed, tone: 'bad', label: 'failed' }, { count: c.pending, tone: 'pending', label: 'running' }]} />
          <span className="font-mono dash-caption text-fg-muted">{c.passed}/{total}</span>
        </>
      )}
      <span className={`dash-label ${r?.changesRequested ? 'text-danger' : 'text-fg-muted'}`}>
        {r?.changesRequested ? `${r.changesRequested} requested changes` : r?.approved ? `${r.approved} approved` : 'No reviews'}
      </span>
    </div>
  )
}

type TriageOp = 'dismiss' | 'settle' | 'reopen' | { snooze: number }

function triageActions(active: boolean, now: number, busy: boolean, run: (op: TriageOp) => void): OverflowAction[] {
  return active
    ? [
        ...SNOOZE_PRESETS.map((p) => ({ label: `Snooze ${p.label.replace(/^Snooze\s*/i, '').toLowerCase()}`, disabled: busy, onSelect: () => run({ snooze: now + p.ms }) })),
        { label: 'Resolve', disabled: busy, onSelect: () => run('settle') },
        { label: 'Dismiss', disabled: busy, onSelect: () => run('dismiss') },
      ]
    : [{ label: 'Reopen', disabled: busy, onSelect: () => run('reopen') }]
}

interface RowProps {
  selected: boolean
  anySelected: boolean
  onToggle: (i: InboxItem) => void
  onPreview: (i: InboxItem) => void
  item: InboxItem
  now: number
  agentName: (id: string) => string
  onAssign: (i: InboxItem) => void
  onTriage: (i: InboxItem, op: 'dismiss' | 'settle' | 'reopen' | { snooze: number }) => Promise<void>
}

function Row({ item, now, agentName, onAssign, onTriage, selected, anySelected, onToggle, onPreview }: RowProps) {
  const { pr } = item
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<string | null>(null)
  const active = item.status === 'unread' || item.status === 'read'
  const run = async (op: Parameters<RowProps['onTriage']>[1]) => {
    setBusy(true)
    setErr(null)
    try {
      await onTriage(item, op)
    } catch (e) {
      setErr(e instanceof Error ? e.message : 'Could not save')
    } finally {
      setBusy(false)
    }
  }
  const label = `${pr.ref.repo.owner}/${pr.ref.repo.name}#${pr.ref.number}`
  const danger = item.categories.some((c) => DANGER_CATEGORIES.includes(c))
  const reasons = [item.primary, ...item.categories.filter((c) => c !== item.primary)].filter((c): c is InboxCategory => c !== 'other')
  const actions = triageActions(active, now, busy, run)
  return (
    <li onClick={() => onPreview(item)} className={`group flex cursor-pointer items-start gap-3 px-4 py-3 ${selected ? 'bg-surface/50' : 'hover:bg-surface/40'} ${active ? '' : 'opacity-70'}`} data-testid={`pr-row-${item.key}`}>
      <span onClick={(e) => e.stopPropagation()} className={`-m-2.5 flex shrink-0 items-center justify-center p-2.5 md:-m-1 md:p-1 ${anySelected ? '' : 'max-md:hidden md:opacity-0 md:group-focus-within:opacity-100 md:group-hover:opacity-100'}`}>
        <Checkbox aria-label={`Select ${label}`} checked={selected} onCheckedChange={() => onToggle(item)} />
      </span>
      <span aria-hidden className={`mt-0.5 flex size-7 shrink-0 items-center justify-center rounded-full border ${danger ? 'border-danger/60 text-danger' : 'border-border text-fg-muted'}`}>
        <GitPullRequest className="size-3.5" />
      </span>
      <div className="min-w-0 flex-1">
        <button type="button" aria-label={`Preview ${label}: ${pr.title}`} className="block max-w-full truncate text-left dash-body font-medium text-fg">{pr.title}</button>
        <p className="mt-0.5 flex flex-wrap items-center gap-x-3 gap-y-0.5 dash-support text-fg-muted">
          <span className="font-mono" title={label}>{pr.ref.repo.name}#{pr.ref.number}</span>
          <span>{pr.author.login}</span>
          <span>updated {ago(Date.parse(pr.updatedAt), now)}</span>
          {pr.additions !== undefined && <span className="font-mono">+{pr.additions} −{pr.deletions ?? 0}</span>}
          {pr.changedFiles !== undefined && <span className="text-fg-faint">{pr.changedFiles} {pr.changedFiles === 1 ? 'file' : 'files'}</span>}
        </p>
        <p className="mt-1.5 flex flex-wrap items-center gap-1.5">
          {reasons.map((c) => <Chip key={c} tone={DANGER_CATEGORIES.includes(c) ? 'danger' : c === 'unassigned' ? 'faint' : 'neutral'}>{CATEGORY_LABEL[c]}</Chip>)}
          {pr.draft && <Chip>Draft</Chip>}
          {pr.assignees.map((a) => <Chip key={a.login}><InitialsAvatar name={a.login} className="-ml-1 size-4" />{a.login}</Chip>)}
          {item.assignments.map((a) => (
            <Chip key={a.key}>
              <InitialsAvatar name={agentName(a.agentId)} className="-ml-1 size-4" />
              <span title={`message ${a.messageId ?? 'sent'} · ${ago(a.at, now)}`}>{agentName(a.agentId)}: {ASSIGN_TASKS.find((t) => t.id === a.task)?.label.toLowerCase() ?? a.task} · sent</span>
            </Chip>
          ))}
          {item.status === 'snoozed' && item.triage?.snoozeUntil && <Chip tone="faint">snoozed until {new Date(item.triage.snoozeUntil).toLocaleString()}</Chip>}
        </p>
        {err && <p role="alert" className="mt-2 dash-control text-danger">{err}</p>}
      </div>
      <ChecksMeter item={item} />
      <div className={`flex shrink-0 items-center gap-1.5 transition-opacity max-md:hidden group-focus-within:opacity-100 group-hover:opacity-100 ${selected ? 'opacity-100' : 'opacity-0'}`} onClick={(e) => e.stopPropagation()}>
        <Button size="sm" variant="outline" type="button" onClick={() => onAssign(item)} disabled={busy}>{item.assignments.length ? 'Assign again' : 'Assign agent'}</Button>
        <OverflowMenu ariaLabel={`More actions for ${label}`} actions={actions} />
      </div>
    </li>
  )
}

export interface PendingPrInboxProps {
  /** Defaults to the installed forge client (mock unless ?forge=http). */
  forge?: ForgeClient
  /** Poll interval for the list; 0 = load once + manual refresh. Default 60s. */
  pollMs?: number
  staleMs?: number
  initialFilter?: Partial<InboxFilter>
  initialGroupBy?: GroupBy
  /** Agent -> forge login override, see InboxActionDeps. */
  forgeLogin?: InboxActionDeps['forgeLogin']
}

/**
 * Pending-PR inbox. Mount under <ConsoleProvider> (StateStore + ActionBus) and a
 * router. Triage is stored in the StateStore, assignment goes through the bus.
 */
export function PendingPrInbox({ forge: forgeProp, pollMs = 60_000, staleMs = DEFAULT_STALE_MS, initialFilter, initialGroupBy = 'category', forgeLogin }: PendingPrInboxProps) {
  const forge = forgeProp ?? getForgeClient()
  const { actionBus, stateStore } = useConsoleSeams()
  const { client: messaging } = useMessaging()
  const agentsClient = useAgentsClient()
  const state = useItemStates()
  const now = useNow()
  const { prs, loading, error, refresh, refreshedAt, forgeRefreshedAt, cached } = usePendingPrs(forge, { pollMs })
  const [filter, setFilter] = useState<InboxFilter>({ ...DEFAULT_FILTER, ...initialFilter })
  const [groupBy, setGroupBy] = useState<GroupBy>(initialGroupBy)
  const [assigning, setAssigning] = useState<{ item: InboxItem; task?: AssignTask } | null>(null)
  const [toast, setToast] = useState<string | null>(null)
  const [selected, setSelected] = useState<Set<string>>(new Set())
  const [bulkBusy, setBulkBusy] = useState(false)
  const [bulkAssigning, setBulkAssigning] = useState(false)
  const [previewKey, setPreviewKey] = useState<string | null>(null)
  const [names, setNames] = useState<Record<string, string>>({})
  const [expanded, setExpanded] = useState<Set<string>>(new Set())

  useEffect(() => registerInboxActions(actionBus, { forge, messaging, stateStore, forgeLogin }), [actionBus, forge, messaging, stateStore, forgeLogin])
  useEffect(() => {
    let live = true
    agentsClient.listAgents().then((a) => live && setNames(Object.fromEntries(a.map((x) => [x.identity.id, x.identity.name]))), () => undefined)
    return () => {
      live = false
    }
  }, [agentsClient])
  useEffect(() => {
    if (!toast) return
    const t = setTimeout(() => setToast(null), 4000)
    return () => clearTimeout(t)
  }, [toast])

  const all = useMemo(() => buildItems(prs, state, now, { staleMs }), [prs, state, now, staleMs])
  const views = countByView(all)
  // Reason counts reflect the current triage view and repo, so the chips match the list.
  const inView = useMemo(() => filterItems(all, { ...DEFAULT_FILTER, view: filter.view, repo: filter.repo, search: filter.search }), [all, filter.view, filter.repo, filter.search])
  const cats = countByCategory(inView)
  const items = useMemo(() => filterItems(all, filter), [all, filter])
  const groups = useMemo(() => groupItems(items, groupBy), [items, groupBy])
  const repos = useMemo(() => repoOptions(all), [all])

  const triage: RowProps['onTriage'] = async (i, op) => {
    if (op === 'dismiss') await stateStore.apply({ type: 'dismiss', key: i.triageKey })
    else if (op === 'settle') await stateStore.apply({ type: 'settle', key: i.triageKey, resolution: 'resolved' })
    else if (op === 'reopen') await stateStore.apply({ type: 'reopen', key: i.triageKey })
    else await stateStore.apply({ type: 'snooze', key: i.triageKey, until: op.snooze })
  }
  const toggle = (i: InboxItem) => setSelected((cur) => {
    const next = new Set(cur)
    if (!next.delete(i.key)) next.add(i.key)
    return next
  })
  // Bulk triage runs the same per-item ops; selection that no longer matches the view drops out.
  const picked = items.filter((i) => selected.has(i.key))
  async function bulk(op: 'dismiss' | 'settle' | { snooze: number }) {
    setBulkBusy(true)
    const failed = new Set<string>()
    let lastError = ''
    for (const i of picked) {
      try {
        await triage(i, op)
      } catch (e) {
        failed.add(i.key)
        lastError = e instanceof Error ? e.message : 'Could not save'
      }
    }
    const ok = picked.length - failed.size
    setToast(failed.size ? `${ok} of ${picked.length} updated, ${failed.size} failed: ${lastError}` : `${ok} pull request${ok === 1 ? '' : 's'} updated`)
    setSelected(failed)
    setBulkBusy(false)
  }
  const allSelected = items.length > 0 && picked.length === items.length
  const toggleAll = () => setSelected(allSelected ? new Set() : new Set(items.map((i) => i.key)))
  const set = (p: Partial<InboxFilter>) => setFilter((f) => ({ ...f, ...p }))
  const filtered = filter.categories.length > 0 || !!filter.repo || !!filter.search
  const repoLabel = repos.find((r) => r.id === filter.repo)?.label ?? 'All repos'
  const moreNoun = (id: string) => (groupBy === 'category' && id !== 'other' ? MORE_NOUN[id as InboxCategory] : '')

  return (
    <section data-dashboard-shape="square" aria-label="Pending pull requests" className="flex h-full min-w-0 flex-col bg-bg" onKeyDown={(e) => { if (e.key === 'Escape' && selected.size > 0 && !assigning && !bulkAssigning && !(e.target instanceof HTMLInputElement && e.target.type === 'text')) setSelected(new Set()) }}>
      {/* KIT-GAP: PageHeader has no slot beside the title; the golden puts the "N need attention" chip next to it. */}
      <PageHeader title="Pull requests">
        <Chip>{views.active} need attention</Chip>
        {cached && <Chip>Cached</Chip>}
        {refreshedAt && <span className="dash-label text-fg-muted max-md:hidden">Updated {ago(forgeRefreshedAt ?? refreshedAt, now)}</span>}
        <Button variant="outline" size="icon-sm" aria-label="Refresh" onClick={() => void refresh()} disabled={loading}><RefreshCw className="size-3.5" /></Button>
        <OverflowMenu ariaLabel="More" actions={[
          { label: `${groupBy === 'category' ? '✓ ' : ''}Group by reason`, onSelect: () => setGroupBy('category') },
          { label: `${groupBy === 'repo' ? '✓ ' : ''}Group by repo`, onSelect: () => setGroupBy('repo') },
          { label: `${groupBy === 'none' ? '✓ ' : ''}No grouping`, onSelect: () => setGroupBy('none') },
        ]} />
      </PageHeader>
      <div className="min-h-0 flex-1 overflow-auto">
        <div className="flex flex-wrap items-center gap-2 px-4 pt-3">
          <div className="min-w-0 flex-1 max-md:basis-full">
            <PillTabs label="Triage state" value={filter.view} onChange={(view) => set({ view: view as TriageView })} items={VIEWS.map((v) => ({ value: v.id, label: v.label, count: views[v.id] }))} />
          </div>
          <div className="dash-search-adapter min-w-0 basis-60 max-md:flex-1 md:max-w-xs">
            <SearchInput ariaLabel="Search pull requests" value={filter.search} onChange={(search) => set({ search })} placeholder="Search title, author, #" debounceMs={0} slashToFocus />
          </div>
          <DropdownMenu>
            <DropdownMenuTrigger render={<Button variant="outline" size="sm" aria-label="Repository" />}>{repoLabel} ▾</DropdownMenuTrigger>
            <DropdownMenuContent aria-label="Repository" align="end" data-dashboard-shape="square" className="w-56">
              <DropdownMenuItem onClick={() => set({ repo: '' })}>{filter.repo === '' ? '✓ ' : ''}All repos</DropdownMenuItem>
              {repos.map((r) => <DropdownMenuItem key={r.id} onClick={() => set({ repo: r.id })}>{filter.repo === r.id ? '✓ ' : ''}{r.label}</DropdownMenuItem>)}
            </DropdownMenuContent>
          </DropdownMenu>
        </div>
        <PillTabs
          size="sm"
          className="border-b border-border px-4 pb-3 pt-2"
          label="Needs you"
          value={filter.categories[0] ?? ''}
          onChange={(v) => set({ categories: v ? [v as InboxCategory] : [] })}
          items={[{ value: '', label: 'All', count: inView.length }, ...CATEGORY_ORDER.map((c) => ({ value: c, label: CATEGORY_LABEL[c], count: cats[c] }))]}
        />

        {picked.length > 0 && (
          <div role="toolbar" aria-label="Bulk actions" className="sticky top-0 z-10 flex flex-wrap items-center gap-2 border-b border-border bg-surface px-4 py-2">
            <Checkbox aria-label="Select all" checked={allSelected} onCheckedChange={toggleAll} />
            <span className="dash-control font-medium text-fg">{picked.length} selected</span>
            <Button size="sm" disabled={bulkBusy} onClick={() => setBulkAssigning(true)}>Assign to agent ▾</Button>
            <DropdownMenu>
              <DropdownMenuTrigger render={<Button variant="outline" size="sm" disabled={bulkBusy} />}>Snooze ▾</DropdownMenuTrigger>
              <DropdownMenuContent aria-label="Snooze for" align="start" data-dashboard-shape="square" className="w-36">
                {SNOOZE_PRESETS.map((p) => <DropdownMenuItem key={p.id} onClick={() => void bulk({ snooze: now + p.ms })}>{p.label}</DropdownMenuItem>)}
              </DropdownMenuContent>
            </DropdownMenu>
            <Button variant="outline" size="sm" disabled={bulkBusy} onClick={() => void bulk('settle')}>Resolve</Button>
            <Button variant="outline" size="sm" disabled={bulkBusy} onClick={() => void bulk('dismiss')}>Dismiss</Button>
            <Button variant="ghost" size="sm" className="ml-auto" disabled={bulkBusy} onClick={() => setSelected(new Set())}>Clear</Button>
          </div>
        )}
        {error && (
          <p role="alert" className="border-b border-danger/40 px-4 py-2 dash-control text-danger">
            Could not load pending PRs: {error}
          </p>
        )}
        {toast && <p role="status" className="border-b border-border px-4 py-2 dash-control text-fg-muted">{toast}</p>}

        {loading && prs.length === 0 && !error ? (
          <p className="px-4 py-8 text-center dash-control text-fg-faint">Loading pending PRs…</p>
        ) : groups.length === 0 && error ? null : groups.length === 0 ? (
          <div className="py-16">
            {all.length === 0 || (!filtered && filter.view === 'active')
              ? <EmptyState variant="empty" title="No pull requests need you" description="New failures, reviews and conflicts will appear here." />
              : <EmptyState variant="no-results" title="No pull requests match this view" description="Try another state, reason or repository." action={filtered ? { label: 'Clear filters', onClick: () => setFilter((f) => ({ ...DEFAULT_FILTER, view: f.view })) } : undefined} />}
          </div>
        ) : (
          groups.map((g) => {
            const open = expanded.has(g.id)
            const shown = open ? g.items : g.items.slice(0, COLLAPSE_AFTER)
            return (
              <div key={g.id} role="group" aria-label={g.label}>
                {groupBy !== 'none' && (
                  <h3 className="flex items-center justify-between px-4 pb-1 pt-4 font-mono dash-caption font-normal uppercase tracking-label text-fg-muted">
                    <span>{g.label}</span>
                    <span className="text-fg-faint">{g.items.length}</span>
                  </h3>
                )}
                <ul>
                  {shown.map((i) => (
                    <Row key={`${repoKey(i.pr.ref.repo)}#${i.pr.ref.number}`} item={i} now={now} agentName={(id) => names[id] ?? id} onAssign={(i) => setAssigning({ item: i })} onTriage={triage} selected={selected.has(i.key)} anySelected={selected.size > 0} onToggle={toggle} onPreview={(x) => setPreviewKey(x.key)} />
                  ))}
                </ul>
                {g.items.length > COLLAPSE_AFTER && (
                  <Button variant="ghost" size="sm" className="mx-4 my-2 text-fg-muted" aria-expanded={open} onClick={() => setExpanded((cur) => { const n = new Set(cur); if (!n.delete(g.id)) n.add(g.id); return n })}>
                    {open ? 'Show fewer' : `+ ${g.items.length - COLLAPSE_AFTER} more ${moreNoun(g.id)}`.trim()}
                  </Button>
                )}
              </div>
            )
          })
        )}
      </div>
      {previewKey && all.find((i) => i.key === previewKey) && (
        <PrPreviewDrawer
          item={all.find((i) => i.key === previewKey)!}
          forge={forge}
          actions={triageActions(['unread', 'read'].includes(all.find((i) => i.key === previewKey)!.status), now, false, (op) => { const i = all.find((x) => x.key === previewKey)!; setPreviewKey(null); void triage(i, op) })}
          onClose={() => setPreviewKey(null)}
          onAssign={(i) => { setPreviewKey(null); setAssigning({ item: i }) }}
          onAskReview={(i) => { setPreviewKey(null); setAssigning({ item: i, task: 'review' }) }}
        />
      )}
      {bulkAssigning && picked.length > 0 && (
        <AssignDialog pr={picked[0].pr} existing={picked[0].assignments} also={picked.slice(1).map((i) => ({ pr: i.pr, existing: i.assignments }))} onClose={() => setBulkAssigning(false)} onDone={(m) => { setToast(m); setSelected(new Set()); void refresh() }} />
      )}
      {assigning && <AssignDialog pr={assigning.item.pr} existing={assigning.item.assignments} initialTask={assigning.task} onClose={() => setAssigning(null)} onDone={(m) => { setToast(m); void refresh() }} />}
    </section>
  )
}

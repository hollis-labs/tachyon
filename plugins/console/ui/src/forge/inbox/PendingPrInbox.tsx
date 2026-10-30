import { DashboardViewTabs, DashboardToolbar } from '@hollis-labs/kit-dashboard/layout'
import { RecordTag } from '@hollis-labs/kit-dashboard/data'
import { Button, SearchInput, DropdownMenu, DropdownMenuTrigger, DropdownMenuContent, DropdownMenuItem } from '@hollis-labs/design-components'
import { PageHeader } from '@hollis-labs/kit-dashboard'
import { useEffect, useMemo, useState } from 'react'
import { Link } from 'react-router-dom'
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
import { prWorkspaceHref } from './href'
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
import { ASSIGN_TASKS } from './assign'
import { useAgentsClient } from '../../agents/context'

const CATEGORY_TONE: Record<InboxCategory, string> = {
  failing_checks: 'border-danger/60 text-danger',
  changes_requested: 'border-border text-fg',
  conflicts: 'border-border text-fg',
  needs_review: 'border-border text-fg',
  stale: 'border-border text-fg-muted',
  unassigned: 'border-border text-fg-muted',
}

const VIEWS: { id: Exclude<TriageView, 'all'>; label: string }[] = [
  { id: 'active', label: 'Active' },
  { id: 'snoozed', label: 'Snoozed' },
  { id: 'dismissed', label: 'Dismissed' },
  { id: 'settled', label: 'Resolved' },
]

function Pill({ category }: { category: InboxCategory }) {
  return <RecordTag className={`!font-sans !normal-case !tracking-normal ${CATEGORY_TONE[category]}`}>{CATEGORY_LABEL[category]}</RecordTag>
}

function Checks({ item }: { item: InboxItem }) {
  const c = item.pr.checksSummary
  const r = item.pr.reviewSummary
  if (!c && !r) return null
  const parts: string[] = []
  if (c) {
    if (c.failed) parts.push(`${c.failed} failing`)
    if (c.pending) parts.push(`${c.pending} running`)
    if (!c.failed && !c.pending) parts.push('Checks passing')
  }
  return (
    <span className="text-fg-muted">
      {c && <span title="checks" className={c.failed ? 'text-danger' : ''}>{parts.join(', ')}</span>}
      {c && r && ' · '}
      {r && <span title="reviews">{r.approved} approved{r.changesRequested ? `, ${r.changesRequested} blocking` : ''}</span>}
    </span>
  )
}

interface RowProps {
  selected: boolean
  onToggle: (i: InboxItem) => void
  onPreview: (i: InboxItem) => void
  item: InboxItem
  now: number
  agentName: (id: string) => string
  onAssign: (i: InboxItem) => void
  onTriage: (i: InboxItem, op: 'dismiss' | 'settle' | 'reopen' | { snooze: number }) => Promise<void>
}

function Row({ item, now, agentName, onAssign, onTriage, selected, onToggle, onPreview }: RowProps) {
  const { pr } = item
  const [menu, setMenu] = useState(false)
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<string | null>(null)
  const active = item.status === 'unread' || item.status === 'read'
  const run = async (op: Parameters<RowProps['onTriage']>[1]) => {
    setBusy(true)
    setErr(null)
    try {
      await onTriage(item, op)
      setMenu(false)
    } catch (e) {
      setErr(e instanceof Error ? e.message : 'Could not save')
    } finally {
      setBusy(false)
    }
  }
  const label = `${pr.ref.repo.owner}/${pr.ref.repo.name}#${pr.ref.number}`
  return (
    <li className={`group border-b border-border/80 px-4 py-3 ${active ? '' : 'opacity-70'} ${selected ? 'bg-surface' : ''}`} data-testid={`pr-row-${item.key}`}>
      <div className="flex flex-wrap items-start gap-x-4 gap-y-2">
        <input type="checkbox" aria-label={`Select ${label}`} checked={selected} onChange={() => onToggle(item)} className="mt-1 accent-primary" />
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
            <Link to={prWorkspaceHref(pr.ref)} className="font-mono dash-body text-fg underline-offset-2 hover:underline">
              {label}
            </Link>
            {item.categories.map((c) => <Pill key={c} category={c} />)}
            {item.status === 'snoozed' && item.triage?.snoozeUntil && <span className="font-mono dash-caption text-fg-muted">snoozed until {new Date(item.triage.snoozeUntil).toLocaleString()}</span>}
          </div>
          <button type="button" onClick={() => onPreview(item)} aria-label={`Preview ${label}: ${pr.title}`} className="mt-0.5 block max-w-full truncate text-left dash-body text-fg-muted hover:text-fg hover:underline">{pr.title}</button>
          <p className="mt-1 flex flex-wrap items-center gap-x-3 gap-y-0.5 dash-support text-fg-muted">
            <span>by {pr.author.login}</span>
            <span>updated {ago(Date.parse(pr.updatedAt), now)}</span>
            {pr.additions !== undefined && <span><span>+{pr.additions}</span> <span className="text-danger">-{pr.deletions ?? 0}</span></span>}
            <Checks item={item} />
          </p>
          {(pr.assignees.length > 0 || item.assignments.length > 0) && (
            <p className="mt-1 flex flex-wrap items-center gap-1.5 font-mono dash-support text-fg-muted">
              {pr.assignees.map((a) => <span key={a.login} className="border border-border px-1">{a.login}</span>)}
              {item.assignments.map((a) => (
                <span key={a.key} className="border border-border px-1 text-fg-muted" title={`message ${a.messageId ?? 'sent'} · ${ago(a.at, now)}`}>
                  {agentName(a.agentId)}: {ASSIGN_TASKS.find((t) => t.id === a.task)?.label.toLowerCase() ?? a.task} · sent
                </span>
              ))}
            </p>
          )}
        </div>

        <div className={`flex items-center justify-end gap-1.5 transition-opacity group-focus-within:opacity-100 group-hover:opacity-100 ${selected ? 'opacity-100' : 'md:opacity-0'}`}>
          <Button variant="outline" size="sm" type="button" className="max-md:hidden" onClick={() => onAssign(item)} disabled={busy}>
            {item.assignments.length ? 'Assign again' : 'Assign'}
          </Button>
          <DropdownMenu open={menu} onOpenChange={setMenu}>
            <DropdownMenuTrigger render={<Button variant="ghost" size="sm" aria-label={`More actions for ${label}`} disabled={busy} />}>⋯</DropdownMenuTrigger>
            <DropdownMenuContent aria-label="Row actions" align="end" data-dashboard-shape="square" className="w-44">
              <DropdownMenuItem className="md:hidden" onClick={() => onAssign(item)}>{item.assignments.length ? 'Assign again' : 'Assign'}</DropdownMenuItem>
              {active ? (
                <>
                  {SNOOZE_PRESETS.map(p => <DropdownMenuItem key={p.id} disabled={busy} onClick={() => void run({ snooze: now + p.ms })}>Snooze {p.label.replace(/^Snooze\s*/i, '').toLowerCase()}</DropdownMenuItem>)}
                  <DropdownMenuItem disabled={busy} onClick={() => void run('settle')}>Resolve</DropdownMenuItem>
                  <DropdownMenuItem disabled={busy} onClick={() => void run('dismiss')}>Dismiss</DropdownMenuItem>
                </>
              ) : (
                <DropdownMenuItem disabled={busy} onClick={() => void run('reopen')}>Reopen</DropdownMenuItem>
              )}
            </DropdownMenuContent>
          </DropdownMenu>
        </div>
      </div>
      {err && <p role="alert" className="mt-2 font-mono dash-control text-danger">{err}</p>}
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
  const { prs, loading, error, refresh, refreshedAt } = usePendingPrs(forge, { pollMs })
  const [filter, setFilter] = useState<InboxFilter>({ ...DEFAULT_FILTER, ...initialFilter })
  const [groupBy, setGroupBy] = useState<GroupBy>(initialGroupBy)
  const [assigning, setAssigning] = useState<InboxItem | null>(null)
  const [toast, setToast] = useState<string | null>(null)
  const [selected, setSelected] = useState<Set<string>>(new Set())
  const [bulkBusy, setBulkBusy] = useState(false)
  const [bulkAssigning, setBulkAssigning] = useState(false)
  const [previewKey, setPreviewKey] = useState<string | null>(null)
  const [names, setNames] = useState<Record<string, string>>({})

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
  // Category counts reflect the current triage view, so the chips match the list.
  const inView = useMemo(() => filterItems(all, { ...DEFAULT_FILTER, view: filter.view }), [all, filter.view])
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
    const filtered = filter.categories.length > 0 || !!filter.repo || !!filter.search || filter.unassignedOnly

  return (
    <section data-dashboard-shape="square" aria-label="Pending pull requests" className="flex h-full min-w-0 flex-col bg-bg" onKeyDown={(e) => { if (e.key === 'Escape' && selected.size > 0 && !assigning && !bulkAssigning && !(e.target instanceof HTMLInputElement && e.target.type === 'text')) setSelected(new Set()) }}>
      <PageHeader title="Pull requests">
        <Button variant="outline" size="sm" className="dash-header-action" onClick={() => void refresh()} disabled={loading}>{loading ? 'Loading…' : 'Refresh'}</Button>
      </PageHeader>
      <p className="border-b border-border px-4 py-2 dash-support text-fg-muted">
        {views.active} need attention{refreshedAt ? ` · loaded ${ago(refreshedAt, now)}` : ''}
      </p>
      <div className="min-h-0 flex-1 overflow-auto">
      <div className="[&_[role=tab]]:font-sans [&_[role=tab]]:normal-case [&_[role=tab]]:tracking-normal [&_[role=tab]_*]:font-sans [&_[role=tab]_*]:normal-case [&_[role=tab]_*]:tracking-normal">
      <DashboardViewTabs variant="segments" label="Triage state" value={filter.view} onChange={view => set({ view: view as TriageView })} items={VIEWS.map(v => ({ value: v.id, label: v.label, count: views[v.id] }))} />
      </div>

      <DashboardToolbar className="border-b border-border px-4 py-2">
        <DropdownMenu>
          <DropdownMenuTrigger render={<Button variant="outline" size="sm" aria-label="Needs you" />}>
            Needs you: {filter.categories.length === 1 ? CATEGORY_LABEL[filter.categories[0]].toLowerCase() : 'all'}
          </DropdownMenuTrigger>
          <DropdownMenuContent aria-label="Needs you" align="start" data-dashboard-shape="square" className="w-52">
            <DropdownMenuItem onClick={() => set({ categories: [] })}>{filter.categories.length === 0 ? '✓ ' : ''}All reasons</DropdownMenuItem>
            {CATEGORY_ORDER.map(c => <DropdownMenuItem key={c} onClick={() => set({ categories: [c] })}>{filter.categories[0] === c && filter.categories.length === 1 ? '✓ ' : ''}{CATEGORY_LABEL[c]} {cats[c]}</DropdownMenuItem>)}
          </DropdownMenuContent>
        </DropdownMenu>
        <DropdownMenu>
          <DropdownMenuTrigger render={<Button variant="outline" size="sm" aria-label="Filters" />}>
            Filters{(filter.repo ? 1 : 0) + (groupBy !== 'category' ? 1 : 0) > 0 ? ` · ${(filter.repo ? 1 : 0) + (groupBy !== 'category' ? 1 : 0)}` : ''}
          </DropdownMenuTrigger>
          <DropdownMenuContent aria-label="Filters" align="start" data-dashboard-shape="square" className="w-56">
            <DropdownMenuItem onClick={() => set({ repo: '' })}>{filter.repo === '' ? '✓ ' : ''}All repos</DropdownMenuItem>
            {repos.map(r => <DropdownMenuItem key={r.id} onClick={() => set({ repo: r.id })}>{filter.repo === r.id ? '✓ ' : ''}{r.label}</DropdownMenuItem>)}
            <DropdownMenuItem onClick={() => setGroupBy('category')}>{groupBy === 'category' ? '✓ ' : ''}Group by reason</DropdownMenuItem>
            <DropdownMenuItem onClick={() => setGroupBy('repo')}>{groupBy === 'repo' ? '✓ ' : ''}Group by repo</DropdownMenuItem>
            <DropdownMenuItem onClick={() => setGroupBy('none')}>{groupBy === 'none' ? '✓ ' : ''}No grouping</DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
        <div className="dash-search-adapter min-w-0 flex-1 basis-40">
          <SearchInput ariaLabel="Search pull requests" value={filter.search} onChange={search => set({ search })} placeholder="Search title, author, #" debounceMs={0} slashToFocus={false} />
        </div>
        {filtered && <Button variant="ghost" size="sm" onClick={() => setFilter(f => ({ ...DEFAULT_FILTER, view: f.view }))}>Clear</Button>}
      </DashboardToolbar>

      {picked.length > 0 && (
        <div role="toolbar" aria-label="Bulk actions" className="sticky top-0 z-10 flex flex-wrap items-center gap-2 border-b border-border bg-surface px-4 py-2">
          <span className="dash-control text-fg">{picked.length} selected</span>
          <Button variant="outline" size="sm" disabled={bulkBusy} onClick={() => setBulkAssigning(true)}>Assign to agent</Button>
          <DropdownMenu>
            <DropdownMenuTrigger render={<Button variant="outline" size="sm" disabled={bulkBusy} />}>Snooze</DropdownMenuTrigger>
            <DropdownMenuContent aria-label="Snooze for" align="start" data-dashboard-shape="square" className="w-36">
              {SNOOZE_PRESETS.map(p => <DropdownMenuItem key={p.id} onClick={() => void bulk({ snooze: now + p.ms })}>{p.label}</DropdownMenuItem>)}
            </DropdownMenuContent>
          </DropdownMenu>
          <Button variant="outline" size="sm" disabled={bulkBusy} onClick={() => void bulk('settle')}>Resolve</Button>
          <Button variant="outline" size="sm" disabled={bulkBusy} onClick={() => void bulk('dismiss')}>Dismiss</Button>
          <Button variant="ghost" size="sm" disabled={bulkBusy} onClick={() => setSelected(new Set())}>Clear</Button>
        </div>
      )}
      {error && (
        <p role="alert" className="border-b border-danger/40 bg-danger/5 px-4 py-2 font-mono dash-control text-danger">
          Could not load pending PRs: {error}
        </p>
      )}
      {toast && <p role="status" className="border-b border-success/40 bg-success/5 px-4 py-2 font-mono dash-control text-success">{toast}</p>}

      {loading && prs.length === 0 && !error ? (
        <p className="px-4 py-8 text-center font-mono dash-control text-fg-faint">Loading pending PRs…</p>
      ) : groups.length === 0 ? (
        <p className="px-4 py-8 text-center font-mono dash-control text-fg-faint">
          {all.length === 0 ? 'Nothing pending. All PRs are reviewed, green and assigned.' : filtered || filter.view !== 'active' ? 'No pull requests match this view.' : 'Inbox clear.'}
        </p>
      ) : (
        <>
        <label className="flex items-center gap-2 border-b border-border px-4 py-1.5 dash-support text-fg-muted">
          <input type="checkbox" aria-label="Select all" checked={allSelected} onChange={toggleAll} className="accent-primary" />
          Select all
        </label>
        {groups.map((g) => (
          <div key={g.id} role="group" aria-label={g.label}>
            {groupBy !== 'none' && (
              <h3 className="border-b border-border bg-surface/40 px-4 py-1.5 font-sans dash-support font-medium normal-case tracking-normal text-fg-muted">
                {g.label} <span className="text-fg-faint">{g.items.length}</span>
              </h3>
            )}
            <ul>
              {g.items.map((i) => (
                <Row key={`${repoKey(i.pr.ref.repo)}#${i.pr.ref.number}`} item={i} now={now} agentName={(id) => names[id] ?? id} onAssign={setAssigning} onTriage={triage} selected={selected.has(i.key)} onToggle={toggle} onPreview={(x) => setPreviewKey(x.key)} />
              ))}
            </ul>
          </div>
        ))}
        </>
      )}

      </div>
      {previewKey && all.find((i) => i.key === previewKey) && (
        <PrPreviewDrawer item={all.find((i) => i.key === previewKey)!} forge={forge} onClose={() => setPreviewKey(null)} onAssign={(i) => { setPreviewKey(null); setAssigning(i) }} />
      )}
      {bulkAssigning && picked.length > 0 && (
        <AssignDialog pr={picked[0].pr} existing={picked[0].assignments} also={picked.slice(1).map((i) => ({ pr: i.pr, existing: i.assignments }))} onClose={() => setBulkAssigning(false)} onDone={(m) => { setToast(m); setSelected(new Set()); void refresh() }} />
      )}
      {assigning && <AssignDialog pr={assigning.pr} existing={assigning.assignments} onClose={() => setAssigning(null)} onDone={(m) => { setToast(m); void refresh() }} />}
    </section>
  )
}

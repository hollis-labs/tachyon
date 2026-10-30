import { DashboardViewTabs, DashboardToolbar } from '@hollis-labs/kit-dashboard/layout'
import { FilterChipGroup, RecordTag } from '@hollis-labs/kit-dashboard/data'
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
  return <RecordTag className={`font-mono ${CATEGORY_TONE[category]}`}>{CATEGORY_LABEL[category]}</RecordTag>
}

function Checks({ item }: { item: InboxItem }) {
  const c = item.pr.checksSummary
  const r = item.pr.reviewSummary
  if (!c && !r) return null
  return (
    <span className="font-mono dash-support text-fg-muted">
      {c && (
        <span title="checks">
          <span>{c.passed}✓</span> <span className={c.failed ? 'text-danger' : ''}>{c.failed}✗</span> <span>{c.pending}…</span>
        </span>
      )}
      {c && r && ' · '}
      {r && <span title="reviews">{r.approved} approved{r.changesRequested ? `, ${r.changesRequested} blocking` : ''}</span>}
    </span>
  )
}

interface RowProps {
  selected: boolean
  onToggle: (i: InboxItem) => void
  item: InboxItem
  now: number
  agentName: (id: string) => string
  onAssign: (i: InboxItem) => void
  onTriage: (i: InboxItem, op: 'dismiss' | 'settle' | 'reopen' | { snooze: number }) => Promise<void>
}

function Row({ item, now, agentName, onAssign, onTriage, selected, onToggle }: RowProps) {
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
    <li className={`border-b border-border/80 px-4 py-3 ${active ? '' : 'opacity-70'}`} data-testid={`pr-row-${item.key}`}>
      <div className="flex flex-wrap items-start gap-x-4 gap-y-2">
        <input type="checkbox" aria-label={`Select ${label}`} checked={selected} onChange={() => onToggle(item)} className="mt-1 accent-primary" />
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
            <Link to={prWorkspaceHref(pr.ref)} className="font-mono dash-body text-fg underline-offset-2 hover:text-fg hover:underline">
              {label}
            </Link>
            {item.categories.map((c) => <Pill key={c} category={c} />)}
            {item.status === 'snoozed' && item.triage?.snoozeUntil && <span className="font-mono dash-caption text-fg-muted">snoozed until {new Date(item.triage.snoozeUntil).toLocaleString()}</span>}
          </div>
          <p className="mt-0.5 truncate dash-body text-fg-muted">{pr.title}</p>
          <p className="mt-1 flex flex-wrap items-center gap-x-3 gap-y-0.5 font-mono dash-support text-fg-muted">
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

        <div className="relative flex flex-wrap items-center justify-end gap-1.5">
          <Button variant="outline" size="sm" type="button" className="dash-label" onClick={() => onAssign(item)} disabled={busy}>
            {item.assignments.length ? 'Assign again' : 'Assign'}
          </Button>
          {active ? (
            <>
              <DropdownMenu open={menu} onOpenChange={setMenu}>
                <DropdownMenuTrigger render={<Button variant="outline" size="sm" className="dash-label" disabled={busy} />}>Snooze</DropdownMenuTrigger>
                <DropdownMenuContent aria-label="Snooze for" align="end" data-dashboard-shape="square" className="w-36">
                  {SNOOZE_PRESETS.map(p => <DropdownMenuItem key={p.id} disabled={busy} onClick={() => void run({ snooze: now + p.ms })}>{p.label}</DropdownMenuItem>)}
                </DropdownMenuContent>
              </DropdownMenu>
              <Button variant="outline" size="sm" type="button" className="dash-label" onClick={() => run('settle')} disabled={busy}>Resolve</Button>
              <Button variant="outline" size="sm" type="button" className="dash-label" onClick={() => run('dismiss')} disabled={busy}>Dismiss</Button>
            </>
          ) : (
            <Button variant="outline" size="sm" type="button" className="dash-label" onClick={() => run('reopen')} disabled={busy}>Reopen</Button>
          )}

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
    try {
      for (const i of picked) await triage(i, op)
      setToast(`${picked.length} pull request${picked.length === 1 ? '' : 's'} updated`)
      setSelected(new Set())
    } catch (e) {
      setToast(e instanceof Error ? e.message : 'Could not save')
    } finally {
      setBulkBusy(false)
    }
  }
  const set = (p: Partial<InboxFilter>) => setFilter((f) => ({ ...f, ...p }))
  const toggleCat = (c: InboxCategory) => set({ categories: filter.categories.includes(c) ? filter.categories.filter((x) => x !== c) : [...filter.categories, c] })
  const filtered = filter.categories.length > 0 || !!filter.repo || !!filter.search || filter.unassignedOnly

  return (
    <section data-dashboard-shape="square" data-dashboard-accent="yellow" aria-label="Pending pull requests" className="flex h-full min-w-0 flex-col bg-bg">
      <PageHeader title="Pull requests">
        <Button variant="outline" size="sm" className="dash-header-action" onClick={() => void refresh()} disabled={loading}>{loading ? 'Loading…' : 'Refresh'}</Button>
      </PageHeader>
      <p className="border-b border-border px-4 py-2 font-mono dash-support text-fg-muted">
        {views.active} need attention{refreshedAt ? ` · loaded ${ago(refreshedAt, now)}` : ''}
      </p>
      <div className="min-h-0 flex-1 overflow-auto">
      <DashboardViewTabs variant="segments" label="Triage state" value={filter.view} onChange={view => set({ view: view as TriageView })} items={VIEWS.map(v => ({ value: v.id, label: v.label, count: views[v.id] }))} />

      <DashboardToolbar className="border-b border-border px-4 py-2">
        <FilterChipGroup variant="outline" ariaLabel="Reasons" chips={CATEGORY_ORDER.map(c => ({ value: c, label: <>{CATEGORY_LABEL[c]} {cats[c]}</>, activeClassName: 'border-fg bg-surface text-fg' }))} selected={filter.categories} onToggle={value => toggleCat(value as InboxCategory)} />
        <span className="mx-1 hidden h-4 w-px bg-surface-hover sm:block" />
        <select aria-label="Repository" value={filter.repo} onChange={e => set({ repo: e.target.value })}>
          <option value="">All repos</option>
          {repos.map(r => <option key={r.id} value={r.id}>{r.label}</option>)}
        </select>
        <select aria-label="Group by" value={groupBy} onChange={e => setGroupBy(e.target.value as GroupBy)}>
          <option value="category">Group: reason</option>
          <option value="repo">Group: repo</option>
          <option value="none">No grouping</option>
        </select>
        <div className="dash-search-adapter min-w-0 flex-1 basis-40">
          <SearchInput ariaLabel="Search pull requests" value={filter.search} onChange={search => set({ search })} placeholder="Search title, author, #" debounceMs={0} slashToFocus={false} />
        </div>
        <label className="flex items-center gap-1.5 font-mono dash-support text-fg-muted">
          <input type="checkbox" checked={filter.unassignedOnly} onChange={e => set({ unassignedOnly: e.target.checked })} className="accent-primary" />
          Unassigned only
        </label>
        {filtered && <Button variant="outline" size="sm" className="dash-label" onClick={() => setFilter(f => ({ ...DEFAULT_FILTER, view: f.view }))}>Clear</Button>}
      </DashboardToolbar>

      {picked.length > 0 && (
        <div role="toolbar" aria-label="Bulk actions" className="flex flex-wrap items-center gap-2 border-b border-border bg-surface px-4 py-2">
          <span className="dash-control text-fg">{picked.length} selected</span>
          {SNOOZE_PRESETS.slice(0, 1).map(p => <Button key={p.id} variant="outline" size="sm" disabled={bulkBusy} onClick={() => void bulk({ snooze: now + p.ms })}>Snooze {p.label.toLowerCase()}</Button>)}
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
        groups.map((g) => (
          <div key={g.id} role="group" aria-label={g.label}>
            {groupBy !== 'none' && (
              <h3 className="border-b border-border bg-surface/40 px-4 py-1.5 font-mono dash-caption uppercase tracking-widest text-fg-muted">
                {g.label} <span className="text-fg-faint">{g.items.length}</span>
              </h3>
            )}
            <ul>
              {g.items.map((i) => (
                <Row key={`${repoKey(i.pr.ref.repo)}#${i.pr.ref.number}`} item={i} now={now} agentName={(id) => names[id] ?? id} onAssign={setAssigning} onTriage={triage} selected={selected.has(i.key)} onToggle={toggle} />
              ))}
            </ul>
          </div>
        ))
      )}

      </div>
      {assigning && <AssignDialog pr={assigning.pr} existing={assigning.assignments} onClose={() => setAssigning(null)} onDone={(m) => { setToast(m); void refresh() }} />}
    </section>
  )
}

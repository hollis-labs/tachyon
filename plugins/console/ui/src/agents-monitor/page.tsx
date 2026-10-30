import { useEffect, useMemo, useState } from 'react'
import { useNavigate, useParams, useSearchParams } from 'react-router-dom'
import { EmptyState, Skeleton } from '@hollis-labs/design-components'
import { PageHeader } from '@hollis-labs/kit-dashboard'
import { FilterChipGroup } from '@hollis-labs/kit-dashboard/data'
import { DashboardSearchToolbar, DashboardViewTabs } from '@hollis-labs/kit-dashboard/layout'
import { SegmentedProgress } from '@planner/shared'
import { TeamOperations } from '../agents/TeamOperations'
import { Btn } from '../agents/ui'
import { LaunchAgentDialog } from '../fleet/LaunchAgentDialog'
import { useAgentsMonitor } from './data'
import { SessionModal } from './session-modal'
import { ageText } from './session-parts'
import { SessionBoard, SessionCards, SessionRows, matchesFilter, matchesQuery, useViewState, type GroupBy, type SessionView } from './sessions-view'
import { AGENTS, type FilterId } from './vocabulary'
import type { SessionState } from './types'

type Tab = 'sessions' | 'activity' | 'teams'
const TABS: readonly Tab[] = ['sessions', 'activity', 'teams']
const GROUPS: readonly GroupBy[] = ['workroot', 'agent', 'none']

const typing = (t: EventTarget | null) => {
  const el = t as HTMLElement | null
  return !!el && (el.isContentEditable || ['INPUT', 'TEXTAREA', 'SELECT'].includes(el.tagName))
}

/** The Agents section: Sessions (live monitor), Activity (bus events, later), Teams (the existing page). */
export function AgentsPage() {
  const [params, setParams] = useSearchParams()
  const tab: Tab = TABS.includes(params.get('tab') as Tab) ? (params.get('tab') as Tab) : 'sessions'
  const setTab = (t: Tab) => setParams(t === 'sessions' ? {} : { tab: t })
  const { data, status, error, refetch } = useAgentsMonitor()
  const [view, setView] = useViewState<SessionView>('agents.view', 'rows', ['rows', 'cards', 'board'])
  const [groupBy, setGroupBy] = useState<GroupBy>('workroot')
  const [filter, setFilter] = useState<FilterId>('all')
  const [query, setQuery] = useState('')
  const navigate = useNavigate()
  const { session: openId } = useParams<{ session?: string }>()
  const openSession = (id: string) => navigate(`/agents/${encodeURIComponent(id)}${params.toString() ? `?${params}` : ''}`)
  const openRow = data?.sessions.find((x) => x.id === openId)
  const [launchOpen, setLaunchOpen] = useState(false)
  const now = Date.now()
  const stale = status === 'reconnecting'

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.metaKey || e.ctrlKey || e.altKey || typing(e.target) || document.querySelector('[role="dialog"]')) return
      const i = ['1', '2', '3'].indexOf(e.key)
      if (i >= 0) return setTab(TABS[i])
      if (tab !== 'sessions') return
      if (e.key === 'r') setView('rows')
      else if (e.key === 'c') setView('cards')
      else if (e.key === 'b') setView('board')
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }) // eslint-disable-line react-hooks/exhaustive-deps

  const visible = useMemo(() => (data?.sessions ?? []).filter((s) => matchesFilter(s, filter) && matchesQuery(s, query)), [data, filter, query])
  const counts = useMemo(() => {
    const c = { working: 0, waiting: 0, idle: 0, stale: 0, unknown: 0, ...data?.summary } as Record<SessionState, number>
    return c
  }, [data])

  return (
    <div className="flex h-full min-w-0 flex-col">
      <PageHeader title={AGENTS.title}>
        {data && (
          <span className="dash-caption text-fg-muted">
            {data.collector.collecting ? AGENTS.collecting(data.collector.sessions, data.collector.last_event_at ? ageText(now - data.collector.last_event_at) : '—') : AGENTS.notCollecting}
          </span>
        )}
      </PageHeader>
      <div className="flex items-center gap-2 border-b border-border px-4 py-2">
        <DashboardViewTabs label={AGENTS.tabsLabel} value={tab} onChange={(v) => setTab(v as Tab)} items={TABS.map((t) => ({ value: t, label: AGENTS.tabs[t] }))} />
        <Btn tone="primary" className="ml-auto" onClick={() => setLaunchOpen(true)}>{AGENTS.launch}</Btn>
      </div>

      {tab === 'teams' && <div className="flex-1 overflow-auto"><TeamOperations /></div>}
      {tab === 'activity' && <div className="flex-1 overflow-auto p-4 dash-support text-fg-muted">{AGENTS.tabs.activity}</div>}
      {tab === 'sessions' && (
        <>
          <div className="flex flex-wrap items-center gap-3 border-b border-border px-4 py-2">
            <div className="min-w-48 flex-1">
              <SegmentedProgress
                label={AGENTS.summaryText(counts)}
                items={[{ state: 'doing', count: counts.working }, { state: 'review', count: counts.waiting }, { state: 'todo', count: counts.idle + counts.stale + counts.unknown }]}
              />
            </div>
            {/* The page's one live region: counts per state, polite. */}
            <p role="status" aria-live="polite" className="dash-caption text-fg-muted">{AGENTS.summaryText(counts)}</p>
          </div>
          {stale && <p role="status" className="border-b border-border px-4 py-2 dash-caption text-fg-muted">{AGENTS.reconnecting}</p>}
          <DashboardSearchToolbar value={query} onChange={setQuery} label={AGENTS.searchLabel} placeholder={AGENTS.searchPlaceholder} slashToFocus>
            <FilterChipGroup ariaLabel={AGENTS.filtersLabel} variant="outline" chips={(Object.keys(AGENTS.filters) as FilterId[]).map((f) => ({ value: f, label: AGENTS.filters[f] }))} selected={[filter]} onToggle={(v) => setFilter(v as FilterId)} />
            <div className="hidden items-center gap-2 sm:flex">
              <label className="dash-caption text-fg-muted" htmlFor="agents-group">{AGENTS.groupBy}</label>
              <select id="agents-group" value={groupBy} onChange={(e) => setGroupBy(e.target.value as GroupBy)} className="rounded-md border border-border bg-panel px-2 py-1 dash-caption text-fg">
                {GROUPS.map((g) => <option key={g} value={g}>{AGENTS.group[g]}</option>)}
              </select>
              <DashboardViewTabs variant="segments" label={AGENTS.viewLabel} value={view} onChange={(v) => setView(v as SessionView)} items={(['rows', 'cards', 'board'] as const).map((v) => ({ value: v, label: AGENTS.views[v] }))} />
            </div>
          </DashboardSearchToolbar>
          <div className="min-h-0 flex-1 overflow-auto">
            {status === 'error' ? (
              <EmptyState variant="error" title={AGENTS.loadFailed} description={error ?? ''} action={{ label: AGENTS.retry, onClick: () => void refetch() }} />
            ) : data === null ? (
              <div className="flex flex-col gap-2 p-4" aria-busy>{[0, 1, 2, 3].map((i) => <Skeleton key={i} className="h-14 w-full" />)}</div>
            ) : data.sessions.length === 0 ? (
              <EmptyState variant="empty" title={AGENTS.empty} description="" action={{ label: AGENTS.launch, onClick: () => setLaunchOpen(true) }} />
            ) : visible.length === 0 ? (
              <EmptyState variant="empty" title={AGENTS.noMatch} description="" action={{ label: AGENTS.clearFilters, onClick: () => (setFilter('all'), setQuery('')) }} />
            ) : (
              <div className={stale ? 'opacity-60' : undefined}>
                {view === 'rows' && <SessionRows sessions={visible} groupBy={groupBy} now={now} selectedId={openId} onSelect={openSession} />}
                {view === 'cards' && <SessionCards sessions={visible} now={now} onSelect={openSession} />}
                {view === 'board' && <SessionBoard sessions={visible} now={now} onSelect={openSession} />}
              </div>
            )}
          </div>
        </>
      )}
      {openRow && (
        <SessionModal
          key={openRow.id}
          session={openRow}
          paused={stale}
          now={now}
          onClose={() => navigate(`/agents${params.toString() ? `?${params}` : ''}`)}
          onRestarted={(next) => (void refetch(), navigate(next ? `/agents/${encodeURIComponent(next)}` : '/agents'))}
        />
      )}
      <LaunchAgentDialog open={launchOpen} onClose={() => setLaunchOpen(false)} onLaunched={() => void refetch()} />
    </div>
  )
}

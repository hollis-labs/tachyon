import { useEffect, useMemo, useState } from 'react'
import { useNavigate, useParams, useSearchParams } from 'react-router-dom'
import { EmptyState, Skeleton, Tabs, TabsList, TabsTrigger } from '@hollis-labs/design-components'
import { FilterChipGroup } from '@hollis-labs/kit-dashboard/data'
import { DashboardSearchToolbar, DashboardViewTabs } from '@hollis-labs/kit-dashboard/layout'
import { SegmentedProgress } from '@planner/shared'
import { TeamOperations } from '../agents/TeamOperations'
import { Btn } from '../agents/ui'
import { LaunchAgentDialog } from '../fleet/LaunchAgentDialog'
import { useAgentsMonitor } from './data'
import { ActivityTab } from './activity-tab'
import { AgentsErrorBoundary } from './error-boundary'
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
  const { data, status, error, refetch, busTick } = useAgentsMonitor()
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

  const all = data?.sessions ?? []
  const filterCount = { all: null, working: all.filter((x) => matchesFilter(x, 'working')).length, needs_you: all.filter((x) => matchesFilter(x, 'needs_you')).length, stale: all.filter((x) => matchesFilter(x, 'stale')).length }
  const filterLabel = (f: FilterId) => (filterCount[f] === null || !data ? AGENTS.filters[f] : `${AGENTS.filters[f]} ${filterCount[f]}`)
  const visible = useMemo(() => (data?.sessions ?? []).filter((s) => matchesFilter(s, filter) && matchesQuery(s, query)), [data, filter, query])
  const counts = useMemo(() => {
    const c = { working: 0, waiting: 0, idle: 0, stale: 0, unknown: 0, ...data?.summary } as Record<SessionState, number>
    return c
  }, [data])

  return (
    <div className="flex h-full min-w-0 flex-col">
      <div className="flex h-12 shrink-0 items-center gap-2.5 border-b border-border-subtle px-4">
        <h1 className="font-mono text-caption font-semibold uppercase leading-none tracking-label text-fg">{AGENTS.title}</h1>
        <Tabs value={tab} onValueChange={(v) => setTab(v as Tab)} className="min-w-0 overflow-x-auto [scrollbar-width:none]">
          <TabsList aria-label={AGENTS.tabsLabel} className="h-auto flex-nowrap gap-0.5 bg-transparent p-0">
            {TABS.map((t) => (
              <TabsTrigger key={t} value={t} className="h-7 gap-1.5 rounded-control px-3 text-xs font-normal text-fg-muted data-[active]:bg-selection data-[active]:font-semibold data-[active]:text-fg">
                {AGENTS.tabs[t]}
                {t === 'sessions' && data && <small className="font-mono text-caption text-fg-faint">{data.sessions.length}</small>}
                {t === 'activity' && <small className="font-mono text-caption text-fg-faint">{AGENTS.live}</small>}
              </TabsTrigger>
            ))}
          </TabsList>
        </Tabs>
        <span className="flex-1" />
        {data && (
          <span className="hidden items-center gap-1.5 font-mono text-label text-fg-secondary sm:flex">
            <i aria-hidden className="size-1.5 rounded-full bg-fg/85" />
            {data.collector.collecting ? AGENTS.collecting(data.collector.sessions, data.collector.last_event_at ? ageText(now - data.collector.last_event_at) : AGENTS.na) : AGENTS.notCollecting}
          </span>
        )}
        <Btn tone="primary" className="shrink-0" onClick={() => setLaunchOpen(true)}>{AGENTS.launch}</Btn>
      </div>

      {tab !== 'teams' && (
        <div className="flex flex-wrap items-center gap-x-4 gap-y-2 border-b border-border-subtle px-4 py-2.5 text-xs text-fg-secondary">
          {([['working', counts.working], ['waiting', counts.waiting], ['idle', counts.idle], ['unknown', counts.unknown]] as const).map(([k, n]) => (
            <span key={k} className="flex items-baseline gap-1.5"><b className="font-mono text-base font-semibold tabular-nums text-fg">{data ? n : AGENTS.na}</b> {k}</span>
          ))}
          {counts.stale > 0 && <span className="flex items-baseline gap-1.5"><b className="font-mono text-base font-semibold tabular-nums text-fg">{counts.stale}</b> stale</span>}
          <div className="w-full sm:w-[200px]">
            <SegmentedProgress
              label={AGENTS.summaryText(counts)}
              items={[{ state: 'done', count: counts.working }, { state: 'blocked', count: counts.waiting }, { state: 'todo', count: counts.idle + counts.stale + counts.unknown }]}
            />
          </div>
          <span className="flex-1" />
          {/* The page's one live region: counts per state, polite. */}
          <p role="status" aria-live="polite" className="sr-only">{AGENTS.summaryText(counts)}</p>
          <span className="ml-auto text-fg-muted sm:ml-0">{AGENTS.streamStatus[status]}</span>
        </div>
      )}
      {tab === 'teams' && <div className="flex-1 overflow-auto"><TeamOperations /></div>}
      {tab === 'activity' && <AgentsErrorBoundary><ActivityTab busTick={busTick} now={now} /></AgentsErrorBoundary>}
      {tab === 'sessions' && (
        <>
          {stale && <p role="status" className="border-b border-border px-4 py-2 dash-caption text-fg-muted">{AGENTS.reconnecting}</p>}
          <DashboardSearchToolbar value={query} onChange={setQuery} label={AGENTS.searchLabel} placeholder={AGENTS.searchPlaceholder} slashToFocus>
            <FilterChipGroup ariaLabel={AGENTS.filtersLabel} variant="outline" chips={(Object.keys(AGENTS.filters) as FilterId[]).map((f) => ({ value: f, label: filterLabel(f), activeClassName: f === 'needs_you' ? 'text-danger' : undefined }))} selected={[filter]} onToggle={(v) => setFilter(v as FilterId)} />
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
              <AgentsErrorBoundary resetKey={`${view}${groupBy}`}>
              <div className={stale ? 'opacity-60' : undefined}>
                {view === 'rows' && <SessionRows sessions={visible} groupBy={groupBy} now={now} selectedId={openId} onSelect={openSession} />}
                {view === 'cards' && <SessionCards sessions={visible} now={now} onSelect={openSession} />}
                {view === 'board' && <SessionBoard sessions={visible} now={now} onSelect={openSession} />}
              </div>
              </AgentsErrorBoundary>
            )}
          </div>
        </>
      )}
      {openRow && (
        <AgentsErrorBoundary resetKey={openRow.id} onClose={() => navigate(`/agents${params.toString() ? `?${params}` : ''}`)}>
        <SessionModal
          key={openRow.id}
          session={openRow}
          paused={stale}
          now={now}
          onClose={() => navigate(`/agents${params.toString() ? `?${params}` : ''}`)}
          onRestarted={(next) => (void refetch(), navigate(next ? `/agents/${encodeURIComponent(next)}` : '/agents'))}
        />
        </AgentsErrorBoundary>
      )}
      <LaunchAgentDialog open={launchOpen} onClose={() => setLaunchOpen(false)} onLaunched={() => void refetch()} />
    </div>
  )
}

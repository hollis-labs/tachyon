import { useEffect, useMemo, useState } from 'react'
import { useNavigate, useParams, useSearchParams } from 'react-router-dom'
import { Dialog, DialogContent, DialogDescription, DialogTitle, EmptyState, Skeleton, Tabs, TabsList, TabsTrigger } from '@hollis-labs/design-components'
import { DashboardSearchToolbar } from '@hollis-labs/kit-dashboard/layout'
import { Chip, SegmentedProgress } from '@planner/shared'
import { Btn } from '../agents/ui'
import { LaunchAgentDialog } from '../fleet/LaunchAgentDialog'
import { normalizeDetail, useAgentsMonitor } from './data'
import { useAgentsRead, type AgentsSettings } from './api'
import { ActivityTab } from './activity-tab'
import { AgentsErrorBoundary } from './error-boundary'
import { SessionModal } from './session-modal'
import { TeamsTab } from './teams-tab'
import { ageText } from './session-parts'
import { StaleAfterContext } from './session-rows'
import { SessionBoard, SessionCards, SessionRows, matchesFilter, matchesQuery, useViewState, type GroupBy, type SessionView } from './sessions-view'
import { AGENTS, type FilterId } from './vocabulary'
import type { Session, SessionState } from './types'

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
  const settings = useAgentsRead<AgentsSettings>('/settings')
  const [view, setView] = useViewState<SessionView>('agents.view', 'rows', ['rows', 'cards', 'board'])
  const [groupBy, setGroupBy] = useState<GroupBy>('workroot')
  const [filter, setFilter] = useState<FilterId>('all')
  const [query, setQuery] = useState('')
  const navigate = useNavigate()
  const { session: openId } = useParams<{ session?: string }>()
  const openSession = (id: string) => navigate(`/agents/${encodeURIComponent(id)}${params.toString() ? `?${params}` : ''}`)
  const listed = data?.sessions.find((x) => x.id === openId)
  // A session that ended leaves the list; its link (and a modal that was open on it) still reads it on its own.
  const detail = useAgentsRead<Session>(openId && data && !listed ? `/sessions/${encodeURIComponent(openId)}` : null)
  const openRow = listed ?? (detail.data && detail.data.id === openId ? normalizeDetail(detail.data) : undefined)
  const closeSession = () => navigate(`/agents${params.toString() ? `?${params}` : ''}`)
  const [launchOpen, setLaunchOpen] = useState(false)
  const now = Date.now()
  const stale = status === 'reconnecting'

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.metaKey || e.ctrlKey || e.altKey || typing(e.target) || document.querySelector('[role="dialog"]')) return
      const i = ['1', '2', '3'].indexOf(e.key)
      if (i >= 0) return setTab(TABS[i])
      if (tab !== 'sessions') return
      // j and k reach the list from anywhere on the page; once a row has focus the list moves it.
      if ((e.key === 'j' || e.key === 'k') && !(document.activeElement as HTMLElement | null)?.closest('[data-session-row]')) {
        const first = document.querySelector<HTMLElement>('[data-session-row][tabindex="0"]') ?? document.querySelector<HTMLElement>('[data-session-row]')
        if (first) (e.preventDefault(), first.focus())
        return
      }
      if (e.key === 'r') setView('rows')
      else if (e.key === 'c') setView('cards')
      else if (e.key === 'b') setView('board')
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }) // eslint-disable-line react-hooks/exhaustive-deps

  const all = data?.sessions ?? []
  const filterCount = { all: null, working: all.filter((x) => matchesFilter(x, 'working')).length, needs_you: all.filter((x) => matchesFilter(x, 'needs_you')).length, stale: all.filter((x) => matchesFilter(x, 'stale')).length }
  const visible = useMemo(() => (data?.sessions ?? []).filter((s) => matchesFilter(s, filter) && matchesQuery(s, query)), [data, filter, query])
  const counts = useMemo(() => {
    const c = { working: 0, waiting: 0, idle: 0, stale: 0, unknown: 0, ...data?.summary } as Record<SessionState, number>
    return c
  }, [data])

  return (
    <StaleAfterContext.Provider value={settings.data?.stale_after_s ?? 900}>
    <div className="flex h-full min-w-0 flex-col">
      <div className="flex h-12 shrink-0 items-center gap-2.5 border-b border-border-subtle px-4">
        <h1 className="sr-only font-mono text-caption font-semibold uppercase sm:not-sr-only leading-none tracking-label text-fg">{AGENTS.title}</h1>
        <Tabs value={tab} onValueChange={(v) => setTab(v as Tab)} className="min-w-0 overflow-x-auto [scrollbar-width:none]">
          <TabsList aria-label={AGENTS.tabsLabel} className="h-auto flex-nowrap gap-0.5 bg-transparent p-0">
            {TABS.map((t) => (
              <TabsTrigger key={t} value={t} className="h-7 gap-1.5 rounded-control px-2 text-xs sm:px-3 font-normal text-fg-muted data-[active]:bg-selection data-[active]:font-semibold data-[active]:text-fg">
                {AGENTS.tabs[t]}
                {t === 'sessions' && data && <small className="hidden font-mono text-caption text-fg-faint sm:inline">{data.sessions.length}</small>}
                {t === 'activity' && <small className="hidden font-mono text-caption text-fg-faint sm:inline">{AGENTS.live}</small>}
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
        <Btn tone="primary" className="shrink-0 font-sans normal-case tracking-normal" onClick={() => setLaunchOpen(true)}>{AGENTS.launch}</Btn>
      </div>

      {tab !== 'teams' && (
        <div className="flex flex-wrap items-center gap-x-4 gap-y-2 border-b border-border-subtle px-4 py-2.5 text-xs text-fg-secondary">
          {([['working', counts.working], ['waiting', counts.waiting], ['idle', counts.idle], ['unknown', counts.unknown]] as const).map(([k, n]) => (
            <span key={k} className="flex items-baseline gap-1.5"><b className="font-mono text-base font-semibold tabular-nums text-fg">{data ? `${k === 'working' && n > 0 && (data.sessions ?? []).some((x) => x.state === 'working' && x.state_inferred) ? '~' : ''}${n}` : AGENTS.na}</b> {k}</span>
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
      {tab === 'teams' && <AgentsErrorBoundary><TeamsTab /></AgentsErrorBoundary>}
      {tab === 'activity' && <AgentsErrorBoundary><ActivityTab busTick={busTick} now={now} /></AgentsErrorBoundary>}
      {tab === 'sessions' && (
        <>
          {stale && <p role="status" className="border-b border-border px-4 py-2 dash-caption text-fg-muted">{AGENTS.reconnecting}</p>}
          <DashboardSearchToolbar value={query} onChange={setQuery} label={AGENTS.searchLabel} placeholder={AGENTS.searchPlaceholder} slashToFocus>
            {/* Pill chips with their counts, as the golden draws them (and as the Activity tab does); Needs you is the one accent. */}
            <div role="toolbar" aria-label={AGENTS.filtersLabel} className="flex flex-wrap items-center gap-1.5">
              {(Object.keys(AGENTS.filters) as FilterId[]).map((f) => (
                <button key={f} type="button" aria-pressed={filter === f} onClick={() => setFilter(f)} className="rounded-full">
                  <Chip tone={f === 'needs_you' && (filterCount[f] ?? 0) > 0 ? 'danger' : 'neutral'} selected={filter === f} className="h-6 cursor-pointer hover:bg-selection">{AGENTS.filters[f]}{data && filterCount[f] !== null && <span className="font-mono tabular-nums">{filterCount[f]}</span>}</Chip>
                </button>
              ))}
            </div>
            <div className="hidden items-center gap-2 sm:flex">
              <label className="dash-caption text-fg-muted" htmlFor="agents-group">{AGENTS.groupBy}</label>
              <select id="agents-group" value={groupBy} onChange={(e) => setGroupBy(e.target.value as GroupBy)} className="h-6 rounded-full border border-border bg-transparent px-2 text-label text-fg-secondary">{/* KIT-GAP: a chip-styled select, as on Activity's Apps filter; the golden draws a "Workspace" chip. */}
                {GROUPS.map((g) => <option key={g} value={g}>{AGENTS.group[g]}</option>)}
              </select>
              {/* The golden's switch: one bordered group, sentence case, the active view filled. Kit Tabs, as in the page header; the kit-dashboard view tabs are upper-case and take no class. */}
              <Tabs value={view} onValueChange={(v) => setView(v as SessionView)}>
                <TabsList aria-label={AGENTS.viewLabel} className="h-auto gap-0 rounded-control border border-border bg-transparent p-0">
                  {(['rows', 'cards', 'board'] as const).map((v) => (
                    <TabsTrigger key={v} value={v} className="h-7 rounded-none px-2.5 text-xs font-normal text-fg-muted first:rounded-l-control last:rounded-r-control data-[active]:bg-selection data-[active]:font-semibold data-[active]:text-fg">{AGENTS.views[v]}</TabsTrigger>
                  ))}
                </TabsList>
              </Tabs>
            </div>
          </DashboardSearchToolbar>
          <div tabIndex={0} className="min-h-0 flex-1 overflow-auto outline-none focus-visible:outline focus-visible:outline-1 focus-visible:outline-ring">
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
              {/* Reconnecting: last data, read-only. Greyed, not dimmed: opacity put every row under 4.5:1. */}
              <div className={stale ? 'grayscale' : undefined}>
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
        <AgentsErrorBoundary resetKey={openRow.id} onClose={closeSession}>
        <SessionModal
          key={openRow.id}
          session={openRow}
          paused={stale}
          now={now}
          onClose={closeSession}
          onRestarted={(next) => (void refetch(), navigate(next ? `/agents/${encodeURIComponent(next)}` : '/agents'))}
        />
        </AgentsErrorBoundary>
      )}
      {openId && data && !openRow && detail.error && (
        <Dialog open onOpenChange={(o) => !o && closeSession()}>
          <DialogContent widthClassName="max-w-md">
            <DialogTitle className="sr-only">{AGENTS.sessionGone}</DialogTitle>
            <DialogDescription className="sr-only">{AGENTS.sessionGoneDetail}</DialogDescription>
            <EmptyState variant="no-results" title={AGENTS.sessionGone} description={AGENTS.sessionGoneDetail} action={{ label: AGENTS.backToSessions, onClick: closeSession }} />
          </DialogContent>
        </Dialog>
      )}
      <LaunchAgentDialog open={launchOpen} onClose={() => setLaunchOpen(false)} onLaunched={() => void refetch()} />
    </div>
    </StaleAfterContext.Provider>
  )
}

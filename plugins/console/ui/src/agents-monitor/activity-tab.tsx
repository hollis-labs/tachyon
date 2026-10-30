import { useCallback, useEffect, useMemo, useState } from 'react'
import { Button, Skeleton } from '@hollis-labs/design-components'
import { DashboardSearchToolbar } from '@hollis-labs/kit-dashboard/layout'
import { EventTimeline, type TimelineEvent } from '@planner/shared'
import { agentsGet } from './api'
import { AGENTS } from './vocabulary'

const A = AGENTS.activity

export interface BusEvent {
  id: string
  seq: number
  at: number
  type: 'deploy' | 'restart' | 'task' | 'failure' | 'host' | 'workstream' | 'lifecycle' | 'card'
  subject: string
  actor: string
  app: string
  severity: 'info' | 'warn' | 'error'
  refs: { kind: string; ref: string; label: string }[]
  card: { title: string; summary: string } | null
}
interface BusResponse { events: BusEvent[]; next_before: number | null }

const FACET_OF: Partial<Record<BusEvent['type'], string>> = { deploy: 'deploys', restart: 'restarts', task: 'tasks', failure: 'failures' }
const dayLabel = (at: number, now: number) => {
  const days = Math.floor(now / 86_400_000) - Math.floor(at / 86_400_000)
  return days === 0 ? 'Today' : days === 1 ? 'Yesterday' : new Date(at).toLocaleDateString()
}

/** Bus events as timeline rows. Failures (and errors) are the only coloured rows; refs are named in text. */
export function toBusTimeline(events: readonly BusEvent[], now: number): TimelineEvent[] {
  return events.map((e) => ({
    id: e.id,
    day: dayLabel(e.at, now),
    time: new Date(e.at).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' }),
    facet: FACET_OF[e.type] ?? 'other',
    tone: e.type === 'failure' || e.severity === 'error' ? 'blocked' : 'neutral',
    body: (
      <span>
        {e.subject} <span className="text-fg-faint">{e.app} · {e.actor}</span>
        {e.refs.length > 0 && <span className="ml-2 font-mono dash-caption text-fg-muted">{e.refs.map((r) => r.label).join(' ')}</span>}
      </span>
    ),
    card: e.card ? <span><strong className="text-fg">{e.card.title}</strong> {e.card.summary}</span> : undefined,
  }))
}

/** The Activity tab: what happened across apps, newest first, with "Show more" paging. */
export function ActivityTab({ busTick, now }: { busTick: number; now: number }) {
  const [events, setEvents] = useState<BusEvent[] | null>(null)
  const [next, setNext] = useState<number | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [app, setApp] = useState('')
  const [query, setQuery] = useState('')

  const loadHead = useCallback(async () => {
    try {
      const r = await agentsGet<BusResponse>('/bus/events?limit=50')
      setEvents((old) => {
        // Keep what the user paged in below the new head.
        const seen = new Set(r.events.map((e) => e.id))
        return old ? [...r.events, ...old.filter((e) => !seen.has(e.id) && e.seq < Math.min(...r.events.map((x) => x.seq)))] : r.events
      })
      setNext((n) => n ?? r.next_before)
      setError(null)
    } catch (err) {
      setError(err instanceof Error ? err.message : A.loadFailed)
    }
  }, [])
  useEffect(() => {
    void loadHead()
  }, [loadHead, busTick])

  const more = async () => {
    if (next === null) return
    try {
      const r = await agentsGet<BusResponse>(`/bus/events?limit=50&before_seq=${next}`)
      setEvents((old) => [...(old ?? []), ...r.events])
      setNext(r.next_before)
    } catch (err) {
      setError(err instanceof Error ? err.message : A.loadFailed)
    }
  }

  const apps = useMemo(() => [...new Set((events ?? []).map((e) => e.app))].sort(), [events])
  const shown = useMemo(() => (events ?? []).filter((e) => (!app || e.app === app) && (!query.trim() || `${e.subject} ${e.app} ${e.actor}`.toLowerCase().includes(query.trim().toLowerCase()))), [events, app, query])
  const rows = useMemo(() => toBusTimeline(shown, now), [shown, now])
  const facets = (Object.keys(A.facets) as (keyof typeof A.facets)[]).map((f) => ({ id: f, label: A.facets[f], count: f === 'all' ? rows.length : rows.filter((r) => r.facet === f).length }))

  if (error && events === null) return <p role="alert" className="p-4 dash-support text-danger">{A.loadFailed}: {error} <button type="button" className="underline" onClick={() => void loadHead()}>{A.retry}</button></p>
  return (
    <div className="flex min-h-0 flex-1 flex-col overflow-auto">
      <DashboardSearchToolbar value={query} onChange={setQuery} label={A.searchLabel} placeholder={A.searchPlaceholder}>
        <label className="flex items-center gap-2 dash-caption text-fg-muted">
          {A.appLabel}
          <select value={app} onChange={(e) => setApp(e.target.value)} className="rounded-md border border-border bg-panel px-2 py-1 dash-caption text-fg">
            <option value="">{A.allApps}</option>
            {apps.map((a) => <option key={a} value={a}>{a}</option>)}
          </select>
        </label>
      </DashboardSearchToolbar>
      <div className="p-4">
        {error && <p role="alert" className="mb-2 dash-caption text-danger">{error}</p>}
        {events === null ? (
          <div aria-busy className="flex flex-col gap-2">{[0, 1, 2].map((i) => <Skeleton key={i} className="h-8 w-full" />)}</div>
        ) : (
          <EventTimeline events={rows} facets={facets} allFacet="all" facetsLabel={A.filtersLabel} streamLabel={A.streamLabel} emptyLabel={events.length === 0 ? A.empty : A.noMatch} filteredEmptyLabel={A.noMatch} />
        )}
        {next !== null && <Button className="mt-3" size="sm" variant="outline" onClick={() => void more()}>{A.showMore(50)}</Button>}
      </div>
    </div>
  )
}

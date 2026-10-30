import { useCallback, useEffect, useMemo, useState } from 'react'
import { SearchInput, Skeleton } from '@hollis-labs/design-components'
import { Chip } from '@planner/shared'
import { agentsGet, useAgentsRead } from './api'
import { Cite, EventFeed, clock, dayLabel, type Entry } from './session-events'
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
const TOPIC_OF: Record<string, BusEvent['type']> = { deploys: 'deploy', restarts: 'restart', tasks: 'task', failures: 'failure' }
type TopicState = 'live' | 'off' | 'no_emitter'
const SOURCE_LABEL: Record<string, string> = { deploy: 'deploys', restart: 'restarts', task: 'tasks', failure: 'failures', host: 'host', workstream: 'workstreams', lifecycle: 'lifecycle', card: 'cards' }

/** Bus events as rail entries. Failures (and errors) are the only coloured rows; refs are named in text. */
export function toBusTimeline(events: readonly BusEvent[], now: number): Entry[] {
  return events.map((e) => ({
    id: e.id,
    at: e.at,
    day: dayLabel(e.at, now),
    time: clock(e.at),
    facet: FACET_OF[e.type] ?? 'other',
    type: e.type,
    tone: e.type === 'failure' || e.severity === 'error' ? 'blocked' : 'neutral',
    summary: e.subject,
    body: (
      <span>
        {e.subject}
        {e.refs.length > 0 && !e.card && <span className="ml-1.5 inline-flex gap-1">{e.refs.map((r) => <Cite key={r.ref}>{r.label} ↗</Cite>)}</span>}
        <span className="text-fg-faint"> · {e.app}{e.actor ? ` · ${e.actor}` : ''}</span>
      </span>
    ),
    card: e.card,
    cites: e.refs.map((r) => r.label),
  }))
}

/** The Activity tab: what happened across apps, newest first, with "Show more" paging. */
export function ActivityTab({ busTick, now }: { busTick: number; now: number }) {
  const [events, setEvents] = useState<BusEvent[] | null>(null)
  const [next, setNext] = useState<number | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [app, setApp] = useState('')
  const [query, setQuery] = useState('')
  const [facet, setFacet] = useState('all')
  const collector = useAgentsRead<{ bus_topics: Record<string, TopicState> }>('/collector')

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
  const topics = collector.data?.bus_topics ?? null
  const topicState = (facet: string): TopicState | null => (topics && TOPIC_OF[facet] ? topics[TOPIC_OF[facet]] ?? null : null)
  const count = (f: string) => (f === 'all' ? rows.length : rows.filter((r) => r.facet === f).length)
  const visible = facet === 'all' ? rows : rows.filter((r) => r.facet === facet)
  const connected = topics ? (Object.keys(topics) as string[]).filter((t) => topics[t] === 'live').map((t) => SOURCE_LABEL[t] ?? t) : []
  const notConnected = topics ? (Object.keys(topics) as string[]).filter((t) => topics[t] !== 'live').map((t) => SOURCE_LABEL[t] ?? t) : []
  const emptyCopy = () => {
    if (events !== null && events.length === 0) return A.empty
    const st = topicState(facet)
    if (visible.length === 0 && st === 'no_emitter') return A.noEmitter
    if (visible.length === 0 && st === 'off') return A.topicOff(SOURCE_LABEL[TOPIC_OF[facet]] ?? facet)
    return A.noMatch
  }

  if (error && events === null) return <p role="alert" className="p-4 text-xs text-danger">{A.loadFailed}: {error} <button type="button" className="underline" onClick={() => void loadHead()}>{A.retry}</button></p>
  return (
    <div className="min-h-0 flex-1 overflow-auto">
      <div className="flex max-w-[860px] flex-col gap-3 p-4">
        <div className="flex flex-wrap items-center gap-2">
          <div className="min-w-48 flex-1"><SearchInput value={query} onChange={setQuery} placeholder={A.searchPlaceholder} ariaLabel={A.searchLabel} slashToFocus={false} /></div>
          <div role="toolbar" aria-label={A.filtersLabel} className="flex flex-wrap items-center gap-1.5">
            {(Object.keys(A.facets) as (keyof typeof A.facets)[]).map((f) => (
              <button key={f} type="button" aria-pressed={facet === f} onClick={() => setFacet(f)} className="rounded-full">
                <Chip tone={f === 'failures' && count(f) > 0 ? 'danger' : 'neutral'} selected={facet === f} className="h-6 cursor-pointer hover:bg-selection">{A.facets[f]}{(f === 'all' || count(f) > 0) && <span className="font-mono tabular-nums">{count(f)}</span>}</Chip>
              </button>
            ))}
            {/* KIT-GAP: a chip-styled select (kit Combobox is a heavier control); native select for now. */}
            <select aria-label={A.appLabel} value={app} onChange={(e) => setApp(e.target.value)} className="h-6 rounded-full border border-border bg-transparent px-2 text-label text-fg-secondary">
              <option value="">{A.apps}</option>
              {apps.map((a) => <option key={a} value={a}>{a}</option>)}
            </select>
          </div>
        </div>
        {topics && (
          <p className="text-label text-fg-muted" data-testid="activity-sources">
            {A.sourcesConnected}: {connected.length ? connected.join(', ') : A.none}. {A.sourcesNot}: {notConnected.length ? notConnected.join(', ') : A.none}.
          </p>
        )}
        {error && <p role="alert" className="text-label text-danger">{error}</p>}
        {events === null ? (
          <div aria-busy className="flex flex-col gap-2">{[0, 1, 2].map((i) => <Skeleton key={i} className="h-8 w-full" />)}</div>
        ) : visible.length === 0 ? (
          <p className="py-2 text-xs text-fg-muted">{emptyCopy()}</p>
        ) : (
          <EventFeed entries={visible} now={now} label={A.streamLabel} time="clock" onMore={next !== null ? { label: A.showMore(50), run: () => void more() } : undefined} />
        )}
      </div>
    </div>
  )
}

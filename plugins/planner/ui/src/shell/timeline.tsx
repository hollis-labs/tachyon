import { useCallback, useEffect, useRef, useState } from 'react'
import { Button } from '@hollis-labs/design-components'
import { usePlannerApi } from '@planner/api/context'
import { PlannerApiError } from '@planner/api/errors'
import { FACETS, TEXT } from '@planner/lib/vocabulary'
import { useContributions } from '@planner/lib/regions'
import { EventTimeline, type TimelineEvent } from '@planner/shared'
import type { ThemeTimeline, TimelineEntry } from '@planner/api/types'

const FACET_OF: Record<TimelineEntry['kind'], string | null> = { task: 'tasks', pr: 'prs', decision: 'decisions', blocker: 'blockers', comment: 'comments', workstream: 'workstreams', lifecycle: null, link: null }

/** Relative time: "just now", "12m ago", "3h ago", then the date. */
export function ago(iso: string, now = Date.now()): string {
  const s = Math.max(0, (now - Date.parse(iso)) / 1000)
  if (s < 60) return 'just now'
  if (s < 3600) return `${Math.floor(s / 60)}m ago`
  if (s < 86400) return `${Math.floor(s / 3600)}h ago`
  if (s < 7 * 86400) return `${Math.floor(s / 86400)}d ago`
  return new Date(iso).toLocaleDateString([], { month: 'short', day: 'numeric' })
}

function dayLabel(iso: string, now = Date.now()): string {
  const start = (t: number) => { const d = new Date(t); return new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime() }
  const diff = Math.round((start(now) - start(Date.parse(iso))) / 86_400_000)
  if (diff <= 0) return 'Today'
  if (diff === 1) return 'Yesterday'
  return new Date(iso).toLocaleDateString([], { month: 'short', day: 'numeric' })
}

/** "+1 done", "doing −1, review +1": only the lanes that changed. */
function deltaText(d: NonNullable<TimelineEntry['delta']>): string | undefined {
  const parts = (['done', 'doing', 'review', 'blocked'] as const).filter((k) => d[k]).map((k) => `${k} ${d[k] > 0 ? '+' : '−'}${Math.abs(d[k])}`)
  return parts.length ? parts.join(', ') : undefined
}

function deltaSpoken(d: NonNullable<TimelineEntry['delta']>): string | undefined {
  const parts = (['done', 'doing', 'review', 'blocked'] as const).filter((k) => d[k]).map((k) => `${k[0].toUpperCase()}${k.slice(1)} ${d[k] > 0 ? 'up' : 'down'} ${Math.abs(d[k])}`)
  return parts.length ? parts.join(', ').replace(/, (\w)/g, (_, c: string) => `, ${c.toLowerCase()}`) : undefined
}

export function toTimelineEvent(e: TimelineEntry, now = Date.now()): TimelineEvent {
  const arrow = e.arrow ? TEXT.momentumArrow[e.arrow] : undefined
  return {
    id: String(e.seq),
    day: dayLabel(e.at, now),
    time: ago(e.at, now),
    facet: FACET_OF[e.kind] ?? 'all',
    tone: e.kind === 'blocker' && e.arrow === 'down' ? 'blocked' : undefined,
    body: e.text,
    delta: e.delta ? deltaText(e.delta) : undefined,
    deltaLabel: e.delta ? deltaSpoken(e.delta) : undefined,
    momentum: arrow,
  }
}

export interface TimelineState {
  data: ThemeTimeline | null
  events: TimelineEntry[]
  error: string | null
  loadingMore: boolean
  more: () => void
  retry: () => void
}

/** The theme's timeline: first page on mount, earlier pages on demand. */
export function useThemeTimeline(themeId: string): TimelineState {
  const api = usePlannerApi()
  const [data, setData] = useState<ThemeTimeline | null>(null)
  const [events, setEvents] = useState<TimelineEntry[]>([])
  const [error, setError] = useState<string | null>(null)
  const [loadingMore, setLoadingMore] = useState(false)
  const live = useRef(true)
  useEffect(() => () => { live.current = false }, [])
  const load = useCallback(async () => {
    try {
      const d = await api.plannerThemeTimeline(themeId, { limit: 30 })
      if (!live.current) return
      setData(d)
      setEvents(d.events)
      setError(null)
    } catch (e) {
      if (live.current) setError(e instanceof PlannerApiError && [404, 405].includes(e.status) ? TEXT.timelineUnavailable : TEXT.timelineFailed)
    }
  }, [api, themeId])
  useEffect(() => { setData(null); setEvents([]); void load() }, [load])
  const more = useCallback(() => {
    const before = data?.next_before_seq
    if (before == null || loadingMore) return
    setLoadingMore(true)
    api.plannerThemeTimeline(themeId, { before_seq: before, limit: 30 })
      .then((d) => { if (live.current) { setEvents((cur) => [...cur, ...d.events]); setData((cur) => cur && { ...cur, next_before_seq: d.next_before_seq }) } })
      .catch(() => { if (live.current) setError(TEXT.timelineFailed) })
      .finally(() => live.current && setLoadingMore(false))
  }, [api, themeId, data, loadingMore])
  return { data, events, error, loadingMore, more, retry: () => void load() }
}

export function ThemeTimelineView({ state }: { state: TimelineState }) {
  const sources = useContributions('planner.focus.timeline.source')
  const { data, events, error, loadingMore, more, retry } = state
  if (error && !data) {
    return (
      <p role="alert" className="flex items-center gap-2 dash-support text-fg-muted">
        {error}
        {error === TEXT.timelineFailed && <Button size="sm" variant="outline" onClick={retry}>{TEXT.tasksRetry}</Button>}
      </p>
    )
  }
  if (!data) return <p role="status" className="dash-support text-fg-faint">{TEXT.tasksLoading}</p>
  const rows = [...events.map((e) => toTimelineEvent(e)), ...sources.flatMap((s) => (Array.isArray(s.data) ? (s.data as TimelineEvent[]) : []))]
  const facets = FACETS.map((f) => ({ id: f.id, label: f.label, count: f.id === 'all' ? rows.length : rows.filter((e) => e.facet === f.id).length }))
  const first = events.at(-1)
  return (
    <div className="flex flex-col gap-4">
      <ol aria-label={TEXT.milestones} className="flex flex-wrap gap-x-6 gap-y-1">
        {data.milestones.map((m) => (
          <li key={m.key} className="dash-caption text-fg-muted">
            <span className="text-fg">{TEXT.milestoneLabels[m.key]}</span> · {m.state === 'reached' ? (m.at ? ago(m.at) : 'reached') : m.state === 'next' ? 'next' : 'later'}
          </li>
        ))}
      </ol>
      <EventTimeline events={rows} facets={facets} allFacet="all" facetsLabel={TEXT.timelineFacets} streamLabel={TEXT.timelineStream} emptyLabel={TEXT.timelineEmpty} filteredEmptyLabel={TEXT.timelineFilteredEmpty} />
      {data.next_before_seq != null && <Button size="sm" variant="outline" className="self-start" disabled={loadingMore} onClick={more}>{TEXT.timelineMore}</Button>}
      {data.next_before_seq == null && first && <p className="dash-caption text-fg-faint">{TEXT.historyStarts(new Date(first.at).toLocaleDateString([], { month: 'short', day: 'numeric' }))}</p>}
      {error && <p role="alert" className="dash-caption text-fg-muted">{error}</p>}
    </div>
  )
}

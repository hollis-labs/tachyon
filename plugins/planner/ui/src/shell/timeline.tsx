import { useCallback, useEffect, useRef, useState } from 'react'
import { Button } from '@hollis-labs/design-components'
import { type ReactNode } from 'react'
import { usePlannerApi } from '@planner/api/context'
import { PlannerApiError } from '@planner/api/errors'
import { FACETS, TEXT } from '@planner/lib/vocabulary'
import { useContributions } from '@planner/lib/regions'
import { CAPTION, EventTimeline, MilestoneTrack, ProgressLine, type DotState, type Milestone, type TimelineEvent } from '@planner/shared'
import type { ThemeTimeline, TimelineEntry } from '@planner/api/types'

const FACET_OF: Record<TimelineEntry['kind'], string | null> = { task: 'tasks', pr: 'prs', decision: 'decisions', blocker: 'blockers', comment: 'comments', workstream: 'workstreams', lifecycle: null, link: null }

/** Relative time: "just now", "12m ago", "3h ago", then the date. Used where a single moment is named. */
export function ago(iso: string, now = Date.now()): string {
  const s = Math.max(0, (now - Date.parse(iso)) / 1000)
  if (s < 60) return 'just now'
  if (s < 3600) return `${Math.floor(s / 60)}m ago`
  if (s < 86400) return `${Math.floor(s / 3600)}h ago`
  if (s < 7 * 86400) return `${Math.floor(s / 86400)}d ago`
  return new Date(iso).toLocaleDateString([], { month: 'short', day: 'numeric' })
}

const dayDiff = (iso: string, now: number) => {
  const start = (t: number) => { const d = new Date(t); return new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime() }
  return Math.round((start(now) - start(Date.parse(iso))) / 86_400_000)
}

/** Today, Yesterday, then one Earlier group. */
function dayLabel(iso: string, now = Date.now()): string {
  const diff = dayDiff(iso, now)
  return diff <= 0 ? TEXT.today : diff === 1 ? TEXT.yesterday : TEXT.earlier
}

/** Clock time for the last two days, the date for older events. */
function clock(iso: string, now = Date.now()): string {
  const d = new Date(iso)
  return dayDiff(iso, now) <= 1 ? d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', hour12: false }) : d.toLocaleDateString([], { month: 'short', day: 'numeric' })
}

const TOKEN = /(CW-\d{8}-\d{4}|PR[ -]?\d+|@[\w.-]+)/g

/** Task ids and PR numbers become cite chips; @names become mention chips. */
export function richText(text: string): ReactNode {
  return text.split(TOKEN).map((part, i) =>
    part.startsWith('@') ? <span key={i} className="rounded-sm bg-info/12 px-1 text-fg">{part}</span>
    : /^(CW-|PR)/.test(part) && i % 2 === 1 ? <span key={i} className="mx-0.5 inline-flex h-4.5 items-center rounded-sm border border-border px-1.5 font-mono text-caption text-fg-secondary">{part}</span>
    : part)
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

function dotOf(e: TimelineEntry): DotState {
  if (e.kind === 'blocker') return 'blocked'
  if (e.delta?.done && e.delta.done > 0) return 'done'
  if (e.delta?.review || e.delta?.doing) return 'doing'
  return 'todo'
}

export function toTimelineEvent(e: TimelineEntry, now = Date.now()): TimelineEvent {
  const arrow = e.arrow ? TEXT.momentumArrow[e.arrow] : undefined
  return {
    id: String(e.seq),
    day: dayLabel(e.at, now),
    time: clock(e.at, now),
    facet: FACET_OF[e.kind] ?? 'all',
    tone: e.kind === 'blocker' && e.arrow === 'down' ? 'blocked' : undefined,
    dot: dotOf(e),
    body: richText(e.text),
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
export function useThemeTimeline(themeId: string, tick = 0): TimelineState {
  const api = usePlannerApi()
  const [data, setData] = useState<ThemeTimeline | null>(null)
  const [events, setEvents] = useState<TimelineEntry[]>([])
  const [error, setError] = useState<string | null>(null)
  const [loadingMore, setLoadingMore] = useState(false)
  const live = useRef(true)
  useEffect(() => { live.current = true; return () => { live.current = false } }, [])
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
  // A stream signal refreshes in place: the entries stay on screen until the new ones arrive.
  const first = useRef(true)
  useEffect(() => { if (first.current) { first.current = false; return } void load() }, [tick])
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

/** Done counts at each event, newest first, rebuilt by undoing every delta from today's total. */
function doneSeries(events: readonly TimelineEntry[], done: number): number[] {
  const out = [done]
  let at = done
  for (const e of events) {
    at -= e.delta?.done ?? 0
    out.push(Math.max(0, at))
  }
  return out.reverse()
}

export function ThemeTimelineView({ state, progress }: { state: TimelineState; progress: { done: number; total: number } }) {
  const sources = useContributions('planner.focus.timeline.source')
  const { data, events, error, loadingMore, more, retry } = state
  if (error && !data) {
    return (
      <p role="alert" className="flex items-center gap-2 text-xs text-fg-muted">
        {error}
        {error === TEXT.timelineFailed && <Button size="sm" variant="outline" onClick={retry}>{TEXT.tasksRetry}</Button>}
      </p>
    )
  }
  if (!data) {
    return (
      <div role="status" aria-label={TEXT.tasksLoading} className="flex flex-col gap-4">
        <div aria-hidden className="h-12 animate-pulse rounded-control bg-surface-hover" />
        <div aria-hidden className="h-24 animate-pulse rounded-panel bg-surface-hover" />
        <EventTimeline loading events={[]} facets={[]} allFacet="all" facetsLabel="" streamLabel="" emptyLabel="" filteredEmptyLabel="" />
      </div>
    )
  }
  const rows = [...events.map((e) => toTimelineEvent(e)), ...sources.flatMap((s) => (Array.isArray(s.data) ? (s.data as TimelineEvent[]) : []))]
  const facets = FACETS.map((f) => ({ id: f.id, label: f.label, count: f.id === 'all' ? rows.length : rows.filter((e) => e.facet === f.id).length, tone: f.id === 'blockers' ? ('danger' as const) : undefined }))
  const first = events.at(-1)
  const toHalf = Math.max(0, Math.ceil(progress.total / 2) - progress.done)
  const milestones: Milestone[] = data.milestones.map((m) => ({
    key: m.key,
    label: m.label ?? (TEXT.milestoneLabels as Record<string, string>)[m.key] ?? m.key,
    state: m.state,
    note: m.note ?? (m.state === 'reached' ? TEXT.milestoneDone : m.state === 'next' ? (m.key === 'half_done' ? TEXT.milestoneNextToGo(toHalf) : TEXT.milestoneNext) : TEXT.milestoneLater),
  }))
  const series = doneSeries(events, progress.done)
  return (
    <div className="flex flex-col gap-4">
      <MilestoneTrack label={TEXT.milestones} milestones={milestones} />
      <section aria-label={TEXT.momentum} className="flex items-center gap-4 rounded-panel border border-border px-3.5 py-3 max-sm:flex-col max-sm:items-stretch">
        <div className="flex min-w-52 flex-col gap-1">
          <span className={CAPTION}>{TEXT.momentum}</span>
          <b className="font-mono text-lg font-semibold">{TEXT.momentumGlyph[data.momentum.label]} {TEXT.momentumLabels[data.momentum.label]}</b>
          <span className="text-xs text-fg-muted">{data.momentum.text}</span>
        </div>
        <ProgressLine values={series} className="h-21 min-w-0 flex-1" />
      </section>
      <EventTimeline events={rows} facets={facets} allFacet="all" facetsLabel={TEXT.timelineFacets} streamLabel={TEXT.timelineStream} emptyLabel={TEXT.timelineEmpty} filteredEmptyLabel={TEXT.timelineFilteredEmpty} order={TEXT.newestFirst} />
      {data.next_before_seq != null && <Button size="sm" variant="outline" className="self-start" disabled={loadingMore} onClick={more}>{TEXT.timelineMore}</Button>}
      {data.next_before_seq == null && first && <p className="text-caption text-fg-faint">{TEXT.historyStarts(new Date(first.at).toLocaleDateString([], { month: 'short', day: 'numeric' }))}</p>}
      {error && <p role="alert" className="text-caption text-fg-muted">{error}</p>}
    </div>
  )
}

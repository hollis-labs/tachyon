import { useState, type ReactNode } from 'react'
import { cn } from '@hollis-labs/design-components'

export type TimelineTone = 'neutral' | 'blocked'

export interface TimelineEvent {
  id: string
  /** Day bucket heading, e.g. "Today". Events must arrive newest first. */
  day: string
  time: string
  /** Facet this event belongs to. */
  facet: string
  tone?: TimelineTone
  icon?: ReactNode
  /** Text with cites and mentions already rendered. */
  body: ReactNode
  /** Progress delta caused, e.g. "+1 done". */
  delta?: string
  /** Momentum arrow glyph with its text equivalent. */
  momentum?: { glyph: string; label: string }
  /** Notable events carry a small flat card under the row. */
  card?: ReactNode
}

export interface TimelineFacet {
  id: string
  label: string
  count: number
}

export interface EventTimelineProps {
  events: readonly TimelineEvent[]
  facets: readonly TimelineFacet[]
  /** Facet id meaning "everything". */
  allFacet: string
  facetsLabel: string
  streamLabel: string
  emptyLabel: string
  filteredEmptyLabel: string
  loading?: boolean
  className?: string
}

/** Rail of events grouped by day with facet chips. Blocked rows are the only coloured ones. */
export function EventTimeline({ events, facets, allFacet, facetsLabel, streamLabel, emptyLabel, filteredEmptyLabel, loading, className }: EventTimelineProps) {
  const [facet, setFacet] = useState(allFacet)
  const shown = facet === allFacet ? events : events.filter((e) => e.facet === facet)
  const days = shown.reduce<{ day: string; rows: TimelineEvent[] }[]>((acc, e) => {
    const last = acc[acc.length - 1]
    if (last && last.day === e.day) last.rows.push(e)
    else acc.push({ day: e.day, rows: [e] })
    return acc
  }, [])

  return (
    <div className={cn('flex min-w-0 flex-col gap-3', className)}>
      <div role="toolbar" aria-label={facetsLabel} className="flex gap-1.5 overflow-x-auto">
        {facets.map((f) => (
          <button key={f.id} type="button" aria-pressed={f.id === facet} onClick={() => setFacet(f.id)} className={cn('shrink-0 rounded-full border border-border px-2.5 py-0.5 dash-caption text-fg-muted hover:bg-panel-hover', f.id === facet && 'bg-panel-hover text-fg')}>
            {f.label} <span className="tabular-nums text-fg-faint">{f.count}</span>
          </button>
        ))}
      </div>
      {loading ? (
        <div aria-hidden className="flex flex-col gap-2">
          {[0, 1, 2].map((i) => <div key={i} className="h-6 animate-pulse rounded-md bg-surface-hover" />)}
        </div>
      ) : days.length === 0 ? (
        <p className="py-4 dash-support text-fg-muted">{events.length === 0 ? emptyLabel : filteredEmptyLabel}</p>
      ) : (
        <div aria-label={streamLabel} role="feed" className="flex flex-col">
          {days.map((d) => (
            <section key={d.day} className="flex flex-col">
              <h3 className="sticky top-0 py-1 font-mono dash-caption uppercase tracking-label text-fg-faint">{d.day}</h3>
              <ul className="flex flex-col border-l border-border-subtle pl-4">
                {d.rows.map((e) => (
                  <li key={e.id} className="relative flex flex-col gap-1 py-1.5">
                    <span aria-hidden className={cn('absolute -left-[1.3rem] top-2.5 size-2 rounded-full bg-border', e.tone === 'blocked' && 'bg-danger')}>{e.icon}</span>
                    <div className={cn('flex min-w-0 items-baseline gap-2 dash-support', e.tone === 'blocked' ? 'text-danger' : 'text-fg')}>
                      <time className="shrink-0 font-mono dash-caption tabular-nums text-fg-faint">{e.time}</time>
                      <span className="min-w-0 flex-1">{e.body}</span>
                      {e.delta && <span className="shrink-0 font-mono dash-caption text-fg-muted">{e.delta}</span>}
                      {e.momentum && <span className="shrink-0 dash-caption text-fg-muted" title={e.momentum.label}><span aria-hidden>{e.momentum.glyph}</span><span className="sr-only">{e.momentum.label}</span></span>}
                    </div>
                    {e.card && <div className="ml-12 border-l border-border-subtle pl-3 dash-caption text-fg-muted">{e.card}</div>}
                  </li>
                ))}
              </ul>
            </section>
          ))}
        </div>
      )}
    </div>
  )
}

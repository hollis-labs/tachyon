import { useState, type ReactNode } from 'react'
import { cn } from '@hollis-labs/design-components'
import { CAPTION, Chip, StateDot, type ChipTone, type DotState } from './primitives'

export type TimelineTone = 'neutral' | 'blocked'

export interface TimelineEvent {
  id: string
  /** Day bucket heading, e.g. "Today". Events must arrive newest first. */
  day: string
  time: string
  /** Facet this event belongs to. */
  facet: string
  tone?: TimelineTone
  /** Dot before the text: the kind of progress the event made. */
  dot?: DotState
  /** Text with cites and mentions already rendered. */
  body: ReactNode
  /** A quieter second line under the text. */
  sub?: ReactNode
  /** Progress delta caused, e.g. "+1 done". */
  delta?: string
  /** Spoken form of the delta, e.g. "Doing down 1, review up 1". */
  deltaLabel?: string
  /** Momentum arrow glyph with its text equivalent. */
  momentum?: { glyph: string; label: string }
}

export interface TimelineFacet {
  id: string
  label: string
  count: number
  /** `danger` for the one facet that is a problem (Blockers). */
  tone?: ChipTone
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
  /** Right end of the facet row, e.g. "newest first". */
  order?: string
  loading?: boolean
  className?: string
}

/** Facet chips over a newest-first stream grouped by day. Blocked rows are the only coloured ones. */
export function EventTimeline({ events, facets, allFacet, facetsLabel, streamLabel, emptyLabel, filteredEmptyLabel, order, loading, className }: EventTimelineProps) {
  const [facet, setFacet] = useState(allFacet)
  const shown = facet === allFacet ? events : events.filter((e) => e.facet === facet)
  const days = shown.reduce<{ day: string; rows: TimelineEvent[] }[]>((acc, e) => {
    const last = acc[acc.length - 1]
    if (last && last.day === e.day) last.rows.push(e)
    else acc.push({ day: e.day, rows: [e] })
    return acc
  }, [])

  return (
    <div className={cn('flex min-w-0 flex-col gap-1', className)}>
      <div role="toolbar" aria-label={facetsLabel} className="flex items-center gap-1.5 overflow-x-auto pb-2 sm:flex-wrap">
        {facets.map((f) => (
          <button key={f.id} type="button" aria-pressed={f.id === facet} onClick={() => setFacet(f.id)} className="shrink-0 rounded-full">
            <Chip tone={f.tone === 'danger' ? 'danger' : 'neutral'} selected={f.id === facet} className="h-6 cursor-pointer hover:bg-selection">
              {f.label} <span className="font-mono tabular-nums">{f.count}</span>
            </Chip>
          </button>
        ))}
        <span className="flex-1" />
        {order && <span className="shrink-0 text-xs text-fg-muted">{order}</span>}
      </div>
      {loading ? (
        <div aria-hidden className="flex flex-col gap-2">
          {[0, 1, 2, 3].map((i) => <div key={i} className="h-8 animate-pulse rounded-control bg-surface-hover" />)}
        </div>
      ) : days.length === 0 ? (
        <p className="py-4 text-xs text-fg-muted">{events.length === 0 ? emptyLabel : filteredEmptyLabel}</p>
      ) : (
        <div aria-label={streamLabel} role="feed" className="flex flex-col">
          {days.map((d) => (
            <section key={d.day} className="flex flex-col">
              <h3 className={cn('pb-1 pt-3', CAPTION)}>{d.day}</h3>
              <ul className="flex flex-col">
                {d.rows.map((e) => (
                  <li key={e.id} className={cn('grid grid-cols-[3.25rem_0.875rem_1fr_auto] items-start gap-2.5 rounded-control px-1.5 py-2 hover:bg-selection max-sm:grid-cols-[2.75rem_0.75rem_1fr]', e.tone === 'blocked' && 'text-danger')}>
                    <time className="font-mono text-xs tabular-nums text-fg-muted">{e.time}</time>
                    <StateDot state={e.tone === 'blocked' ? 'blocked' : (e.dot ?? 'todo')} className="mt-1.5" />
                    <div className={cn('min-w-0 text-control', e.tone === 'blocked' ? 'text-danger' : 'text-fg')}>
                      {e.body}
                      {e.sub && <div className="text-xs text-fg-muted">{e.sub}</div>}
                    </div>
                    <span className="flex shrink-0 gap-1.5 pt-0.5 font-mono text-caption text-fg-secondary max-sm:hidden">
                      {e.delta && <span aria-label={e.deltaLabel}>{e.delta}</span>}
                      {e.momentum && <span title={e.momentum.label}><span aria-hidden>{e.momentum.glyph}</span><span className="sr-only">{e.momentum.label}</span></span>}
                    </span>
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

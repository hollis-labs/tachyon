import { useState, type ReactNode } from 'react'
import { AlertOctagon, CalendarDays, ChevronDown, ChevronUp, Mail, Plus, Power, RefreshCw, RotateCcw, ShieldCheck, ShieldX, SquareCheck, Terminal, UserRound, Wrench, type LucideIcon } from 'lucide-react'
import { cn } from '@hollis-labs/design-components'
import { CAPTION, Chip } from '@planner/shared'
import { ageText } from './session-parts'
import { AGENTS } from './vocabulary'

const M = AGENTS.modal

export interface ApiEvent { id?: string; type: string; at?: number; started_at?: number; summary: string; actor?: string; calls?: number; events?: ApiEvent[]; run_id?: string; card?: { title: string; summary: string } | null }
export interface Gap { start: number; end: number; reason: string; backfilled: boolean }
export interface EventsResponse { events: ApiEvent[]; gaps: Gap[]; next_before: number | null }

export interface Entry {
  id: string
  at: number
  day: string
  time: string
  facet: string
  type: string
  tone: 'neutral' | 'blocked'
  summary: string
  calls?: number
  children?: ApiEvent[]
  card?: ApiEvent['card']
  /** Replaces the summary text when the source already has parts (bus events). */
  body?: ReactNode
  /** A muted second line: Tether's own summary of the event, when it says more than the row. */
  detail?: string
  /** Chips drawn at the right edge of the mini card: PR and issue refs. */
  cites?: ReactNode
  gap?: string
}

/** A tool, a clock time and a code chip: the target of "Reading <chip>" is rendered as a chip when it is a path or id. */
export function Cite({ children }: { children: ReactNode }) {
  return <code className="rounded-sm bg-surface px-1.5 py-px font-mono text-caption text-fg">{children}</code>
}

/** Events this recent sit under the session timeline's 'Now' marker (the stale window). */
export const NOW_WINDOW_MS = 15 * 60_000
const localDay = (t: number) => { const d = new Date(t); return Date.UTC(d.getFullYear(), d.getMonth(), d.getDate()) / 86_400_000 }
/** Day marker in the local calendar. `recent` splits today into 'Now' and 'Earlier today', as the golden's session timeline does; Activity keeps 'Today'. */
export const dayLabel = (at: number, now: number, recent = false) => {
  const days = localDay(now) - localDay(at)
  if (days === 0) return !recent ? M.today : now - at < NOW_WINDOW_MS ? M.now : M.earlierToday
  return days === 1 ? M.yesterday : new Date(at).toLocaleDateString()
}
/** 24-hour clock time, as the golden writes it (12:02, 21:40). */
export const clock = (at: number) => new Date(at).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', hour12: false })

const FACET_OF: Record<string, string> = { tool: 'tools', message: 'messages', approval: 'approvals', error: 'errors', lifecycle: 'lifecycle', compaction: 'lifecycle', clear: 'lifecycle', task: 'lifecycle', control: 'lifecycle' }

/** Events newest first, with collection gaps put where they happened and never filled in. */
export function toTimeline(d: EventsResponse, now: number): Entry[] {
  const rows: Entry[] = (d.events ?? []).map((e, i) => {
    const at = e.at ?? e.started_at ?? 0
    const isRun = e.type === 'tool_run'
    return {
      id: e.id ?? e.run_id ?? `e${i}`,
      at,
      day: dayLabel(at, now, true),
      time: clock(at),
      facet: isRun ? 'tools' : FACET_OF[e.type] ?? 'lifecycle',
      type: e.type,
      tone: e.type === 'error' ? 'blocked' : 'neutral',
      summary: e.summary,
      calls: e.calls,
      children: e.events ?? undefined,
      card: e.card,
    }
  })
  const gaps: Entry[] = (d.gaps ?? []).filter((g) => !isResync(g)).map((g, i) => ({ id: `gap${i}`, at: g.end, day: dayLabel(g.end, now, true), time: clock(g.end), facet: 'lifecycle', type: 'gap', tone: 'neutral', summary: M.gap(g.reason, clock(g.start), clock(g.end), g.backfilled), gap: g.reason }))
  return [...rows, ...gaps].sort((a, b) => b.at - a.at)
}

/** A gap with no length: the stream asked for a resync at one moment. Drawn once, summed, under the timeline, never inline. */
const isResync = (g: Gap) => g.end - g.start < 1000

/** How many zero-length resyncs the page's gaps hold, and the oldest, or null when there are none. */
export function resyncs(d: EventsResponse): { n: number; since: number } | null {
  const rs = (d.gaps ?? []).filter(isResync)
  return rs.length === 0 ? null : { n: rs.length, since: Math.min(...rs.map((g) => g.start)) }
}

/** The golden's gap line: a mono caption between two dashed rules, indented past the rail. */
export function GapLine({ children, as: As = 'div' }: { children: ReactNode; as?: 'div' | 'li' }) {
  return (
    <As className="my-1.5 ml-9 flex items-center gap-2.5 font-mono text-caption text-fg-muted">
      <span aria-hidden className="flex-1 border-t border-dashed border-border" />
      <span>{children}</span>
      <span aria-hidden className="flex-1 border-t border-dashed border-border" />
    </As>
  )
}

const ICON: Record<string, LucideIcon> = { deploy: RefreshCw, restart: RefreshCw, failure: AlertOctagon, host: Power, workstream: Plus, tool: Wrench, tool_run: Terminal, message: Mail, approval: ShieldCheck, error: AlertOctagon, lifecycle: Power, compaction: RotateCcw, clear: RotateCcw, task: SquareCheck, control: Power, seat: UserRound }

function Glyph({ e }: { e: Entry }) {
  const Icon = e.type === 'approval' && /den/i.test(e.summary) ? ShieldX : ICON[e.type] ?? Power
  return <span aria-hidden className={cn('relative z-10 grid size-6 shrink-0 place-items-center rounded-full border border-border bg-bg-elevated', e.tone === 'blocked' ? 'text-danger' : 'text-fg-secondary')}><Icon className="size-3" /></span>
}

function Row({ e, now, time }: { e: Entry; now: number; time: 'age' | 'clock' }) {
  const [open, setOpen] = useState(false)
  if (e.gap) return <GapLine as="li">{e.summary}</GapLine>
  return (
    <li className="flex flex-col">
      <div className="flex items-start gap-3 py-1.5">
        <Glyph e={e} />
        <div className={cn('min-w-0 flex-1 pt-0.5 text-control', e.tone === 'blocked' ? 'text-danger' : 'text-fg')}>
          <span>{e.body ?? e.summary}</span> <span className="text-fg-muted">· {time === 'clock' ? e.time : ageText(now - e.at)}</span>
          {e.children && e.children.length > 0 && (
            <button type="button" aria-expanded={open} aria-label={open ? M.hideCalls : M.showCalls} onClick={() => setOpen(!open)} className="ml-1 inline-flex align-middle text-fg-muted hover:text-fg">
              {open ? <ChevronUp className="size-3.5" /> : <ChevronDown className="size-3.5" />}
            </button>
          )}
          {e.detail && <div className="text-xs text-fg-muted">{e.detail}</div>}
          {e.card && (
            <div className="mt-2 max-w-sm rounded-panel border border-border bg-bg px-3 py-2 text-xs">
              <div className="flex items-center gap-2"><b className="text-control font-semibold text-fg">{e.card.title}</b><span className="flex-1" />{e.cites}</div>
              <div className="text-fg-muted">{e.card.summary}</div>
            </div>
          )}
          {open && (
            <ul className="mt-1 flex flex-col gap-0.5 text-xs text-fg-muted">
              {e.children?.map((c) => <li key={c.id}>{clock(c.at ?? 0)} {c.summary}</li>)}
            </ul>
          )}
        </div>
      </div>
    </li>
  )
}

/** The event rail: day pills, a glyph per event on a vertical line, age in muted text. `limit` folds the rest behind "Show N more events". */
export function EventFeed({ entries, now, limit, label, time = 'age', onMore }: { entries: readonly Entry[]; now: number; limit?: number; label: string; time?: 'age' | 'clock'; /** When set, the foot button asks the caller for older events instead of unfolding. */ onMore?: { label: string; run: () => void } }) {
  const [more, setMore] = useState(false)
  const shown = limit && !more ? entries.slice(0, limit) : entries
  const days = shown.reduce<{ day: string; rows: Entry[] }[]>((acc, e) => {
    const last = acc[acc.length - 1]
    if (last && last.day === e.day) last.rows.push(e)
    else acc.push({ day: e.day, rows: [e] })
    return acc
  }, [])
  return (
    <div role="log" aria-label={label} className="relative flex flex-col">
      <span aria-hidden className="absolute bottom-3 left-3 top-3 border-l border-border" />
      {days.map((d) => (
        <section key={d.day} className="flex flex-col">
          <h2 className="relative z-10 flex items-center gap-3 py-1.5"><span aria-hidden className="relative z-10 grid size-6 shrink-0 place-items-center rounded-full border border-border bg-bg-elevated text-fg-secondary"><CalendarDays className="size-3" /></span><Chip className={cn('h-5 bg-bg-elevated', CAPTION, 'normal-case tracking-normal')}>{d.day}</Chip></h2>
          <ul className="flex flex-col">{d.rows.map((e) => <Row key={e.id} e={e} now={now} time={time} />)}</ul>
        </section>
      ))}
      {onMore && (
        <button type="button" onClick={onMore.run} className="flex items-center gap-3 py-1.5 text-left text-xs text-fg-secondary hover:text-fg">
          <span aria-hidden className="relative z-10 grid size-6 shrink-0 place-items-center rounded-full border border-border bg-bg-elevated"><ChevronDown className="size-3" /></span>
          {onMore.label}
        </button>
      )}
      {limit && !more && entries.length > limit && (
        <button type="button" onClick={() => setMore(true)} className="flex items-center gap-3 py-1.5 text-left text-xs text-fg-secondary hover:text-fg">
          <span aria-hidden className="relative z-10 grid size-6 shrink-0 place-items-center rounded-full border border-border bg-bg-elevated"><ChevronDown className="size-3" /></span>
          {M.showMore(entries.length - limit)}
        </button>
      )}
    </div>
  )
}

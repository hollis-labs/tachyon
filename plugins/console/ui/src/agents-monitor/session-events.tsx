import { useState, type ReactNode } from 'react'
import { AlertOctagon, ChevronDown, ChevronUp, Mail, Power, RotateCcw, ShieldCheck, ShieldX, SquareCheck, Terminal, Wrench, type LucideIcon } from 'lucide-react'
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
  gap?: string
}

/** A tool, a clock time and a code chip: the target of "Reading <chip>" is rendered as a chip when it is a path or id. */
export function Cite({ children }: { children: ReactNode }) {
  return <code className="rounded-sm bg-surface px-1.5 py-px font-mono text-caption text-fg">{children}</code>
}

const dayLabel = (at: number, now: number) => {
  const days = Math.floor(now / 86_400_000) - Math.floor(at / 86_400_000)
  return days === 0 ? M.today : days === 1 ? M.yesterday : new Date(at).toLocaleDateString()
}
const clock = (at: number) => new Date(at).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })

const FACET_OF: Record<string, string> = { tool: 'tools', message: 'messages', approval: 'approvals', error: 'errors', lifecycle: 'lifecycle', compaction: 'lifecycle', clear: 'lifecycle', task: 'lifecycle', control: 'lifecycle' }

/** Events newest first, with collection gaps put where they happened and never filled in. */
export function toTimeline(d: EventsResponse, now: number): Entry[] {
  const rows: Entry[] = d.events.map((e, i) => {
    const at = e.at ?? e.started_at ?? 0
    const isRun = e.type === 'tool_run'
    return {
      id: e.id ?? e.run_id ?? `e${i}`,
      at,
      day: dayLabel(at, now),
      time: clock(at),
      facet: isRun ? 'tools' : FACET_OF[e.type] ?? 'lifecycle',
      type: e.type,
      tone: e.type === 'error' ? 'blocked' : 'neutral',
      summary: e.summary,
      calls: e.calls,
      children: e.events,
      card: e.card,
    }
  })
  const gaps: Entry[] = d.gaps.map((g, i) => ({ id: `gap${i}`, at: g.end, day: dayLabel(g.end, now), time: clock(g.end), facet: 'lifecycle', type: 'gap', tone: 'neutral', summary: M.gap(g.reason), gap: g.reason }))
  return [...rows, ...gaps].sort((a, b) => b.at - a.at)
}

const ICON: Record<string, LucideIcon> = { tool: Wrench, tool_run: Terminal, message: Mail, approval: ShieldCheck, error: AlertOctagon, lifecycle: Power, compaction: RotateCcw, clear: RotateCcw, task: SquareCheck, control: Power }

function Glyph({ e }: { e: Entry }) {
  const Icon = e.type === 'approval' && /den/i.test(e.summary) ? ShieldX : ICON[e.type] ?? Power
  return <span aria-hidden className={cn('relative z-10 grid size-6 shrink-0 place-items-center rounded-full border border-border bg-bg-elevated', e.tone === 'blocked' ? 'text-danger' : 'text-fg-secondary')}><Icon className="size-3" /></span>
}

function Row({ e, now }: { e: Entry; now: number }) {
  const [open, setOpen] = useState(false)
  if (e.gap) return <li className="ml-9 rounded-control border border-dashed border-border px-3 py-1.5 text-xs italic text-fg-muted">{e.summary}</li>
  return (
    <li className="flex flex-col">
      <div className="flex items-start gap-3 py-1.5">
        <Glyph e={e} />
        <div className={cn('min-w-0 flex-1 pt-0.5 text-control', e.tone === 'blocked' ? 'text-danger' : 'text-fg')}>
          <span>{e.summary}</span> <span className="text-fg-muted">· {ageText(now - e.at)}</span>
          {e.children && e.children.length > 0 && (
            <button type="button" aria-expanded={open} aria-label={open ? M.hideCalls : M.showCalls} onClick={() => setOpen(!open)} className="ml-1 inline-flex align-middle text-fg-muted hover:text-fg">
              {open ? <ChevronUp className="size-3.5" /> : <ChevronDown className="size-3.5" />}
            </button>
          )}
          {e.card && (
            <div className="mt-2 max-w-sm rounded-panel border border-border bg-bg px-3 py-2 text-xs">
              <b className="text-control font-semibold text-fg">{e.card.title}</b>
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
export function EventFeed({ entries, now, limit, label }: { entries: readonly Entry[]; now: number; limit?: number; label: string }) {
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
          <h4 className="relative z-10 flex items-center gap-3 py-1.5"><span aria-hidden className="size-6 shrink-0" /><Chip className={cn('h-5 bg-bg-elevated', CAPTION, 'normal-case tracking-normal')}>{d.day}</Chip></h4>
          <ul className="flex flex-col">{d.rows.map((e) => <Row key={e.id} e={e} now={now} />)}</ul>
        </section>
      ))}
      {limit && !more && entries.length > limit && (
        <button type="button" onClick={() => setMore(true)} className="flex items-center gap-3 py-1.5 text-left text-xs text-fg-secondary hover:text-fg">
          <span aria-hidden className="relative z-10 grid size-6 shrink-0 place-items-center rounded-full border border-border bg-bg-elevated"><ChevronDown className="size-3" /></span>
          {M.showMore(entries.length - limit)}
        </button>
      )}
    </div>
  )
}

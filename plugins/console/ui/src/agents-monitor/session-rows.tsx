import { useMemo, useRef, type KeyboardEvent } from 'react'
import { cn } from '@hollis-labs/design-components'
import { CAPTION, Chip, Kbd } from '@planner/shared'
import { OneLiner, ageText, stateText } from './session-parts'
import { AGENTS } from './vocabulary'
import type { Session } from './types'

export type GroupBy = 'workroot' | 'agent' | 'none'

export const needsYou = (s: Session) => s.needs_you.reason !== null

const groupKey = (s: Session, by: GroupBy) => (by === 'workroot' ? s.workroot : by === 'agent' ? s.agent.name : '')
const groupTitle = (key: string, by: GroupBy = 'none') => (key ? key.split('/').filter(Boolean).pop() ?? key : by === 'none' ? AGENTS.sessionsList : AGENTS.noWorkspace)

/** Two initials for a name of several words, the first letter for a single word. */
export const initials = (name: string) => {
  const w = name.split(/\s+/).filter(Boolean)
  return (w.length > 1 ? w[0][0] + w[1][0] : (w[0] ?? '?')[0]).toUpperCase()
}

export function Avatar({ name, className }: { name: string; className?: string }) {
  return <span aria-hidden className={cn('grid size-8 shrink-0 place-items-center rounded-control bg-surface font-mono text-label font-semibold text-fg-secondary', className)}>{initials(name)}</span>
}

/** The dot beside the one-liner. The state is also written out, so the dot is never the only signal. */
export function SessionDot({ s }: { s: Session }) {
  const tone = needsYou(s) ? 'bg-danger' : s.state === 'working' ? 'bg-fg/90' : s.state === 'idle' ? 'bg-fg-faint/60' : 'border border-fg-muted'
  return <i aria-hidden className={cn('inline-block size-1.5 shrink-0 rounded-full', tone)} />
}

/** Kind, provider and the needs-you or stale chips, in the order the golden draws them. */
export function SessionChips({ s }: { s: Session }) {
  return (
    <span className="flex min-w-0 flex-wrap items-center gap-1">
      <Chip square>{AGENTS.kind[s.kind]}</Chip>
      {s.provider && <Chip square>{s.provider}</Chip>}
      {s.needs_you.reason && <Chip tone="danger" title={s.needs_you.since ? `${AGENTS.sinceLabel} ${ageText(Date.now() - s.needs_you.since)}` : undefined}>{AGENTS.needsYouChip[s.needs_you.reason]}</Chip>}
      {s.state === 'stale' && <Chip tone="warning">{AGENTS.states.stale.toLowerCase()}</Chip>}
      {s.state === 'stale' && s.stale_reason && <Chip tone="warning">{AGENTS.staleReasonChip(s.stale_reason)}</Chip>}
    </span>
  )
}

/** The line under the name: dot plus the one-liner, or the state in words when nothing is running. */
export function SessionLine({ s, now }: { s: Session; now: number }) {
  return (
    <span className="mt-0.5 flex min-w-0 items-center gap-2 text-xs text-fg-secondary">
      <SessionDot s={s} />
      <span className="sr-only">{stateText(s, now)}.</span>
      {s.one_liner ? <OneLiner line={s.one_liner} now={now} fallback={AGENTS.noActivity} /> : <span className="truncate text-fg-secondary">{stateText(s, now)}</span>}
    </span>
  )
}

/** Context use as a bar with the number inside it. Unknown stays an empty track reading n/a, with the reason on hover. */
export function ContextBar({ pct }: { pct: number | null }) {
  if (pct === null) {
    return (
      <span role="progressbar" aria-label={AGENTS.contextLabel(null)} title={AGENTS.contextNa} className="relative block h-4 overflow-hidden rounded-sm bg-surface-hover">
        <b className="absolute right-1.5 top-0 font-mono text-micro font-semibold leading-4 text-fg-faint">{AGENTS.na}</b>
      </span>
    )
  }
  const v = Math.max(0, Math.min(100, pct))
  return (
    <span role="progressbar" aria-label={AGENTS.contextLabel(pct)} aria-valuemin={0} aria-valuemax={100} aria-valuenow={v} className="relative block h-4 overflow-hidden rounded-sm bg-surface-hover">
      <i className={cn('absolute inset-y-0 left-0', v >= 90 ? 'bg-danger/70' : 'bg-fg/50')} style={{ width: `${v}%` }} />
      <b className="absolute right-1.5 top-0 font-mono text-micro font-semibold leading-4 text-fg">{v}%</b>
    </span>
  )
}

/** Per-minute activity as a polyline. Decorative: the one-liner and the state carry the meaning. */
export function Spark({ points }: { points: readonly number[] }) {
  const max = Math.max(1, ...points)
  const n = Math.max(1, points.length - 1)
  const pts = points.map((v, i) => `${((i / n) * 60).toFixed(1)},${(14 - (v / max) * 12).toFixed(1)}`).join(' ')
  return (
    <svg aria-hidden viewBox="0 0 60 16" preserveAspectRatio="none" className="h-4 w-[60px] text-fg-secondary">
      <polyline fill="none" stroke="currentColor" strokeWidth="1.2" points={pts} />
    </svg>
  )
}

function Stat({ value, caption, title }: { value: string; caption: string; title?: string }) {
  return (
    <span className="flex flex-col font-mono text-xs text-fg" title={title}>
      <b className="font-normal">{value}</b>
      <small className="text-micro uppercase tracking-widest text-fg-faint">{caption}</small>
    </span>
  )
}

// Columns follow the golden: 390 = avatar, name, context; 1024 adds uptime; 1440 adds sparkline, last and Open.
const GRID = 'grid grid-cols-[32px_minmax(0,1fr)_70px] items-center gap-3.5 sm:grid-cols-[36px_minmax(0,1fr)_110px_56px] lg:grid-cols-[36px_minmax(0,1fr)_130px_72px_56px_56px_64px]'

export function SessionRow({ s, now, selected, tabbable, onOpen }: { s: Session; now: number; selected: boolean; tabbable: boolean; onOpen: (id: string) => void }) {
  const up = s.runtime_ms > 0 ? ageText(s.runtime_ms) : AGENTS.na
  const last = s.last_activity_at !== null ? ageText(now - s.last_activity_at) : AGENTS.na
  return (
    <li
      aria-label={`${s.agent.name}, ${stateText(s, now)}`}
      aria-current={selected || undefined}
      tabIndex={tabbable ? 0 : -1}
      data-session-row
      onClick={() => onOpen(s.id)}
      onKeyDown={(e) => {
        if (e.key === 'Enter' && e.target === e.currentTarget) onOpen(s.id)
      }}
      className={cn('group relative cursor-pointer border-t border-border-subtle px-4 py-2.5 outline-none hover:bg-selection focus-visible:outline focus-visible:outline-1 focus-visible:outline-ring', GRID, selected && 'bg-selection')}
    >
      <Avatar name={s.agent.name} />
      <span className="flex min-w-0 flex-col">
        <span className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1">
          <b className="min-w-0 text-control font-semibold text-fg sm:truncate">{s.agent.name}</b>
          <SessionChips s={s} />
        </span>
        <SessionLine s={s} now={now} />
      </span>
      <ContextBar pct={s.context.pct} />
      <span className="hidden sm:block lg:hidden"><Stat value={up} caption={AGENTS.uptime} title={s.runtime_ms > 0 ? undefined : AGENTS.naReason} /></span>
      <span className="hidden lg:block"><Spark points={s.spark} /></span>
      <span className="hidden lg:block"><Stat value={up} caption={AGENTS.uptime} title={s.runtime_ms > 0 ? undefined : AGENTS.naReason} /></span>
      <span className="hidden lg:block"><Stat value={last} caption={AGENTS.last} title={s.last_activity_at !== null ? undefined : AGENTS.naReason} /></span>
      <span aria-hidden className="hidden text-right text-label text-fg-secondary opacity-0 group-hover:opacity-100 lg:block" style={selected ? { opacity: 1 } : undefined}>{AGENTS.open} <Kbd className="border-0 px-0">⏎</Kbd></span>
    </li>
  )
}

/** Sessions as flat rows under group headings; J/K and arrows rove focus, Enter opens. */
export function SessionRows({ sessions, groupBy, now, selectedId, onSelect }: { sessions: readonly Session[]; groupBy: GroupBy; now: number; selectedId?: string; onSelect: (id: string) => void }) {
  const root = useRef<HTMLDivElement>(null)
  const groups = useMemo(() => {
    const by = new Map<string, Session[]>()
    for (const s of sessions) by.set(groupKey(s, groupBy), [...(by.get(groupKey(s, groupBy)) ?? []), s])
    return [...by].map(([key, list]) => ({ key, list }))
  }, [sessions, groupBy])

  const onKeyDown = (e: KeyboardEvent<HTMLDivElement>) => {
    if (e.metaKey || e.ctrlKey || e.altKey) return
    const rows = [...(root.current?.querySelectorAll<HTMLElement>('[data-session-row]') ?? [])]
    const at = rows.indexOf(document.activeElement as HTMLElement)
    const k = e.key.toLowerCase()
    const go = (i: number) => (e.preventDefault(), rows[Math.max(0, Math.min(i, rows.length - 1))]?.focus())
    if (k === 'j' || e.key === 'ArrowDown') go(at + 1)
    else if (k === 'k' || e.key === 'ArrowUp') go(at < 0 ? 0 : at - 1)
  }

  const firstId = sessions[0]?.id
  return (
    <div ref={root} onKeyDown={onKeyDown} className="flex min-w-0 flex-col">
      {groups.map((g) => {
        const working = g.list.filter((s) => s.state === 'working').length
        return (
          <section key={g.key || 'all'} aria-label={groupTitle(g.key, groupBy)}>
            {groupBy !== 'none' && (
              <h3 className={cn('flex items-center gap-2.5 px-4 pb-1.5 pt-3.5', CAPTION)}>
                <span>{groupTitle(g.key, groupBy)}</span>
                <span className="text-fg-faint">{AGENTS.sessionCount(g.list.length)}</span>
                <span className="flex-1" />
                <span className="text-fg-faint">{AGENTS.workingCount(working)}</span>
              </h3>
            )}
            <ul aria-label={groupTitle(g.key, groupBy)}>
              {g.list.map((s) => (
                <SessionRow key={s.id} s={s} now={now} selected={s.id === selectedId} tabbable={selectedId ? s.id === selectedId : s.id === firstId} onOpen={onSelect} />
              ))}
            </ul>
          </section>
        )
      })}
    </div>
  )
}

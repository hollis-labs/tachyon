import { useState } from 'react'
import { LaneBoard, type Lane } from '@planner/shared'
import { Avatar, ContextBar, SessionDot, Spark, needsYou } from './session-rows'
import { ageText, stateText } from './session-parts'
import { OneLiner } from './session-parts'
import { AGENTS, type FilterId } from './vocabulary'
import type { Session } from './types'

export type SessionView = 'rows' | 'cards' | 'board'
export { SessionRows, needsYou, type GroupBy } from './session-rows'

export function matchesFilter(s: Session, f: FilterId): boolean {
  if (f === 'working') return s.state === 'working'
  if (f === 'needs_you') return needsYou(s)
  if (f === 'stale') return s.state === 'stale'
  return true
}

export function matchesQuery(s: Session, q: string): boolean {
  const needle = q.trim().toLowerCase()
  if (!needle) return true
  const hay = [s.agent.name, s.workroot, s.provider, s.model ?? '', s.one_liner?.tool ?? '', s.one_liner?.target ?? '', s.assignment?.task_ref ?? '', s.assignment?.theme_ref ?? ''].join(' ').toLowerCase()
  return hay.includes(needle)
}

const lineOf = (s: Session, now: number) => (s.one_liner ? <OneLiner line={s.one_liner} now={now} fallback={AGENTS.noActivity} /> : <span className="truncate text-xs text-fg-secondary">{stateText(s, now)}</span>)

export function SessionCards({ sessions, now, onSelect }: { sessions: readonly Session[]; now: number; onSelect: (id: string) => void }) {
  return (
    <ul aria-label={AGENTS.sessionsList} className="grid grid-cols-[repeat(auto-fill,minmax(300px,1fr))] gap-3 p-4 max-sm:grid-cols-1">
      {sessions.map((s) => (
        <li key={s.id}>
          <button type="button" aria-label={`${s.agent.name}, ${stateText(s, now)}`} onClick={() => onSelect(s.id)} className="group flex w-full flex-col gap-2 rounded-panel border border-border bg-bg-elevated p-3 text-left outline-none hover:bg-selection focus-visible:outline focus-visible:outline-1 focus-visible:outline-ring">
            <span className="flex items-center gap-2.5">
              <Avatar name={s.agent.name} />
              <span className="flex min-w-0 flex-col">
                <b className="truncate text-control font-semibold text-fg">{s.agent.name}</b>
                <span className="text-label text-fg-muted">{AGENTS.kind[s.kind]}{s.provider ? ` · ${s.provider}` : ''}{needsYou(s) && <> · <span className="text-danger">{AGENTS.needsYouChip[s.needs_you.reason!]}</span></>}{s.state === 'stale' && <> · {AGENTS.states.stale.toLowerCase()}{s.stale_reason && ` (${AGENTS.staleReasonChip(s.stale_reason)})`}</>}</span>
              </span>
              <span className="flex-1" />
              <SessionDot s={s} />
            </span>
            <span className="flex min-w-0 items-center gap-2 text-xs text-fg-secondary"><span className="sr-only">{stateText(s, now)}.</span>{lineOf(s, now)}</span>
            <span className="flex items-center gap-2">
              <span className="w-[130px]"><ContextBar pct={s.context.pct} /></span>
              <span className="flex-1" />
              <Spark points={s.spark} />
            </span>
            <span className="flex items-center text-label text-fg-muted">
              <span title={s.runtime_ms > 0 ? undefined : AGENTS.naReason}>{AGENTS.uptime} {s.runtime_ms > 0 ? ageText(s.runtime_ms) : AGENTS.na}</span>
              <span className="flex-1" />
              <span aria-hidden className="text-fg-secondary opacity-0 group-hover:opacity-100">{AGENTS.open} ⏎</span>
            </span>
          </button>
        </li>
      ))}
    </ul>
  )
}

const BOARD_COLS = ['working', 'needs_you', 'idle', 'stale'] as const
const colOf = (s: Session): (typeof BOARD_COLS)[number] => (needsYou(s) ? 'needs_you' : s.state === 'working' ? 'working' : s.state === 'idle' ? 'idle' : 'stale')

/** The assignment line exists only for a session that has one; task n of m only when both are known. */
function Assignment({ s }: { s: Session }) {
  const a = s.assignment
  if (!a || (!a.theme_ref && !a.task_ref)) return null
  return (
    <span className="text-label text-fg-secondary">
      {a.theme_ref && <>{a.theme_ref}{a.task_ref && ' · '}</>}
      {a.task_ref && <b className="font-mono font-medium text-fg">{a.task_ref}</b>}
      {a.index != null && a.count != null && <span className="text-fg-muted"> {a.index}/{a.count}</span>}
    </span>
  )
}

export function SessionBoard({ sessions, now, onSelect }: { sessions: readonly Session[]; now: number; onSelect: (id: string) => void }) {
  const byId = new Map(sessions.map((s) => [s.id, s]))
  const lanes: Lane[] = BOARD_COLS.map((c) => ({
    id: c,
    title: AGENTS.boardCols[c],
    limit: 8,
    cards: sessions.filter((s) => colOf(s) === c).map((s) => ({
      id: s.id,
      content: (
        <button type="button" aria-label={`${s.agent.name}, ${stateText(s, now)}`} onClick={() => onSelect(s.id)} className="flex w-full flex-col gap-1.5 text-left">
          <span className="flex items-center gap-2">
            <Avatar name={s.agent.name} className="size-[22px] text-micro" />
            <b className="min-w-0 truncate text-control font-semibold text-fg">{s.agent.name}</b>
            <span className="flex-1" />
            <span className="text-label text-fg-muted">{s.context.pct === null ? AGENTS.na : `${s.context.pct}%`}</span>
          </span>
          <span className="flex min-w-0 items-center text-xs text-fg-secondary"><span className="sr-only">{stateText(s, now)}.</span>{lineOf(s, now)}</span>
          <Assignment s={s} />
        </button>
      ),
    })),
  }))
  return (
    <LaneBoard
      readOnly
      className="p-4"
      lanes={lanes}
      cardTitle={(id) => byId.get(id)?.agent.name ?? id}
      onMove={() => undefined}
      labels={{ lane: AGENTS.boardLane, empty: AGENTS.boardEmpty, more: (n) => `+${n} more`, pickedUp: () => '', movedOver: () => '', dropped: () => '', cancelled: () => '', pending: '', dragHint: '' }}
    />
  )
}

export function useViewState<T extends string>(key: string, initial: T, allowed: readonly T[]) {
  const [v, setV] = useState<T>(() => {
    const saved = typeof localStorage === 'undefined' ? null : localStorage.getItem(key)
    return allowed.includes(saved as T) ? (saved as T) : initial
  })
  return [v, (next: T) => (localStorage.setItem(key, next), setV(next))] as const
}

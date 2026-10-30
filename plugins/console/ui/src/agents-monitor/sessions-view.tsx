import { useMemo, useState } from 'react'
import { cn } from '@hollis-labs/design-components'
import { LaneBoard, SpineList, type Lane, type SpineGroup } from '@planner/shared'
import { ContextMeter, OneLiner, Sparkline, ageText, stateText } from './session-parts'
import { AGENTS, type FilterId } from './vocabulary'
import type { Session } from './types'

export type SessionView = 'rows' | 'cards' | 'board'
export type GroupBy = 'workroot' | 'agent' | 'none'

export const needsYou = (s: Session) => s.needs_you.reason !== null

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

const groupKey = (s: Session, by: GroupBy) => (by === 'workroot' ? s.workroot : by === 'agent' ? s.agent.name : '')
const groupTitle = (key: string) => (key ? key.split('/').filter(Boolean).pop() ?? key : AGENTS.sessionsList)

function Chips({ s }: { s: Session }) {
  return (
    <span className="flex shrink-0 gap-1">
      <span className="rounded-sm border border-border px-1 dash-caption text-fg-muted">{AGENTS.kind[s.kind]}</span>
      <span className="rounded-sm border border-border px-1 dash-caption text-fg-muted">{s.provider}</span>
    </span>
  )
}

/** One session's body, shared by Rows and Cards: state in words, one-liner, context, activity, uptime. */
function Body({ s, now, wide }: { s: Session; now: number; wide?: boolean }) {
  return (
    <span className="flex min-w-0 flex-col gap-1">
      <span className="flex min-w-0 items-center gap-2">
        <span className={cn('dash-caption', needsYou(s) ? 'text-fg' : 'text-fg-muted')}>{stateText(s, now)}</span>
      </span>
      <OneLiner line={s.one_liner} now={now} fallback={AGENTS.noActivity} />
      <span className="flex items-center gap-3">
        <ContextMeter pct={s.context.pct} />
        {wide && <Sparkline points={s.spark} className="hidden md:block" />}
        <span className="font-mono dash-caption text-fg-faint">{AGENTS.uptime} {ageText(s.runtime_ms)}</span>
      </span>
    </span>
  )
}

export function SessionRows({ sessions, groupBy, now, selectedId, onSelect }: { sessions: readonly Session[]; groupBy: GroupBy; now: number; selectedId?: string; onSelect: (id: string) => void }) {
  const groups = useMemo<SpineGroup[]>(() => {
    const by = new Map<string, Session[]>()
    for (const s of sessions) by.set(groupKey(s, groupBy), [...(by.get(groupKey(s, groupBy)) ?? []), s])
    return [...by].map(([key, list]) => ({
      id: key || 'all',
      title: groupTitle(key),
      items: list.map((s) => ({ id: s.id, title: s.agent.name, flags: <Chips s={s} />, progress: <Body s={s} now={now} wide /> })),
    }))
  }, [sessions, groupBy, now])
  return <SpineList label={AGENTS.sessionsList} groups={groups} selectedId={selectedId} onSelect={onSelect} />
}

export function SessionCards({ sessions, now, onSelect }: { sessions: readonly Session[]; now: number; onSelect: (id: string) => void }) {
  return (
    <ul aria-label={AGENTS.sessionsList} className="grid gap-3 p-4 sm:grid-cols-2 xl:grid-cols-3">
      {sessions.map((s) => (
        <li key={s.id}>
          <button type="button" onClick={() => onSelect(s.id)} className="flex w-full flex-col gap-2 rounded-md border border-border p-3 text-left hover:bg-panel-hover">
            <span className="flex items-center gap-2">
              <span className="min-w-0 flex-1 truncate dash-support font-medium text-fg">{s.agent.name}</span>
              <Chips s={s} />
            </span>
            <Body s={s} now={now} />
          </button>
        </li>
      ))}
    </ul>
  )
}

const BOARD_COLS = ['working', 'needs_you', 'idle', 'stale'] as const
const colOf = (s: Session): (typeof BOARD_COLS)[number] => (needsYou(s) ? 'needs_you' : s.state === 'working' ? 'working' : s.state === 'idle' ? 'idle' : 'stale')

export function SessionBoard({ sessions, now, onSelect }: { sessions: readonly Session[]; now: number; onSelect: (id: string) => void }) {
  const byId = new Map(sessions.map((s) => [s.id, s]))
  const lanes: Lane[] = BOARD_COLS.map((c) => ({
    id: c,
    title: AGENTS.boardCols[c],
    cards: sessions.filter((s) => colOf(s) === c).map((s) => ({
      id: s.id,
      content: (
        <button type="button" onClick={() => onSelect(s.id)} className="flex w-full flex-col gap-1 text-left">
          <span className="dash-support font-medium text-fg">{s.agent.name}</span>
          <Body s={s} now={now} />
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
      labels={{ lane: AGENTS.boardLane, empty: AGENTS.boardEmpty, pickedUp: () => '', movedOver: () => '', dropped: () => '', cancelled: () => '', pending: '', dragHint: '' }}
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

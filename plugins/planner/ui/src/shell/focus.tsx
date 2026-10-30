import { useState } from 'react'
import { ChevronDown, Pin } from 'lucide-react'
import { OverflowMenu, cn } from '@hollis-labs/design-components'
import { themeRowActions } from '@planner/components/planner/theme-board'
import { THEME_SECTIONS } from '@planner/lib/planner'
import { TEXT } from '@planner/lib/vocabulary'
import { useContributions } from '@planner/lib/regions'
import { CAPTION, Chip, InlineEdit, SegmentedProgress, SegmentedSwitch, StateDot, StatusMenu, type DotState } from '@planner/shared'
import { ThemeTimelineView, useThemeTimeline } from '@planner/shell/timeline'
import { TaskBoard } from '@planner/shell/task-board'
import { themeSegments } from '@planner/shell/spine'
import type { ThemeAction, ThemeBoard, ThemeRow } from '@planner/api/types'

const ACCESS_NOTE = 'planner-access-note'
const SECTION_DOT: Record<string, DotState> = { in_flight: 'doing', cleared: 'review', queued: 'review', proposed: 'todo', parked: 'todo', landed: 'done' }
const MOMENTUM_GLYPH = { rising: '▲', steady: '▶', cooling: '▼', stalled: '■' } as const

interface FocusProps {
  /** Changes when the Planner stream says something changed: the board and timeline refetch. */
  tick?: number
  /** Writes are disabled while the connection is down. */
  paused?: boolean
  /** The server refused a write for lack of operator access: controls are off and say why. */
  needsAccess?: boolean
  onRetryAccess?: () => void
  onNeedsAccess?: () => void
  row: ThemeRow
  board: ThemeBoard
  view: 'board' | 'timeline'
  onView: (v: 'board' | 'timeline') => void
  onAction: (row: ThemeRow, action: ThemeAction, args?: { position?: number }) => Promise<void>
  pinned: boolean
  onTogglePin: () => void
  onEdit: (row: ThemeRow, patch: Record<string, unknown>) => Promise<void>
  onLink: () => void
}

/** The open theme: header, progress block with momentum, then the Board or Timeline. */
export function Focus({ tick, paused, needsAccess, onRetryAccess, onNeedsAccess, row, board, view, onView, onAction, pinned, onTogglePin, onEdit, onLink }: FocusProps) {
  const seg = themeSegments(row)
  const panels = useContributions('planner.focus.panel')
  const approved = (board.totals.in_flight ?? 0) + (board.totals.cleared ?? 0)
  const actions = themeRowActions(row.section, row.rank, approved)
  const timeline = useThemeTimeline(row.id, tick)
  const momentum = timeline.data?.momentum
  const [why, setWhy] = useState(false)
  const locked = Boolean(paused || needsAccess)
  const section = THEME_SECTIONS.find((s) => s.key === row.section)
  const counts = row.progress.counts ?? {}
  const done = row.progress.done ?? counts.done ?? 0
  const doing = counts.doing ?? counts.in_progress ?? 0
  const review = counts.review ?? 0
  const total = row.progress.total ?? 0
  const todo = Math.max(0, total - done - doing - review - row.blocked_count)
  const stat = (label: string, n: number, tone?: string) => (
    <div className="flex flex-col gap-1">
      <span className={CAPTION}>{label}</span>
      <b className={cn('font-mono text-xl font-semibold leading-none tabular-nums', tone ?? 'text-fg-secondary')}>{n}</b>
    </div>
  )
  return (
    <section aria-labelledby="focus-title" className="flex min-h-0 min-w-0 flex-1 flex-col gap-5 overflow-y-auto px-6 py-5 max-sm:px-4 max-sm:py-4">
      <header className="flex flex-col gap-2.5">
        <div className="flex flex-wrap items-center gap-2.5">
          <Chip square title={row.id}>{row.id.length > 12 ? row.id.slice(0, 8) : row.id}</Chip>
          <span className={CAPTION}>{TEXT.themeRank(row.rank, approved)}</span>
          <span className="flex-1" />
          <SegmentedSwitch label={TEXT.viewLabel} value={view} onChange={(v) => onView(v === 'timeline' ? 'timeline' : 'board')} items={[{ value: 'board', label: TEXT.board }, { value: 'timeline', label: TEXT.timeline }]} />
          <button type="button" aria-pressed={pinned} onClick={onTogglePin} disabled={locked} className="rounded-full">
            <Chip selected={pinned} className="h-6 cursor-pointer hover:bg-selection"><Pin className="size-3" />{TEXT.pin}</Chip>
          </button>
          <OverflowMenu ariaLabel={TEXT.moreActions} actions={[{ label: TEXT.linkTo, onSelect: onLink, disabled: locked }, { label: TEXT.copyLink, onSelect: () => void navigator.clipboard?.writeText(`${location.origin}/planner/themes/${encodeURIComponent(row.id)}`) }]} />
        </div>
        <div className="flex flex-wrap items-center gap-2.5">
          <InlineEdit as="h1" id="focus-title" label={TEXT.titleLabel} value={row.title} disabled={locked} onCommit={(title) => void onEdit(row, { title })} className="min-w-0 font-sans text-xl font-semibold normal-case leading-tight tracking-normal text-fg" />
          <StatusMenu
            trigger={
              <Chip tone="strong" className="h-6 cursor-pointer">
                <StateDot state={SECTION_DOT[row.section] ?? 'todo'} />
                {section?.title ?? row.status}
                <ChevronDown className="size-3" />
              </Chip>
            }
            triggerLabel={`${section?.title ?? row.status}. ${TEXT.moveTo}`}
            heading={TEXT.moveTo}
            current=""
            options={actions.map((a) => ({ value: a.label, label: a.label }))}
            onSelect={(label) => {
              const a = actions.find((x) => x.label === label)
              if (a) void onAction(row, a.action, a.position === undefined ? undefined : { position: a.position })
            }}
            disabled={actions.length === 0 || locked}
            disabledReasonId={needsAccess ? ACCESS_NOTE : undefined}
          />
          <span className="flex min-w-0 flex-wrap items-center gap-x-1.5 text-xs text-fg-muted">
            {row.accountable && <>{TEXT.accountable} <Mention>{row.accountable}</Mention></>}
            {row.executing_owner && <>{row.accountable && '·'} {TEXT.exec} <Mention>{row.executing_owner}</Mention></>}
          </span>
        </div>
        {row.tldr && <InlineEdit as="p" multiline label={TEXT.descriptionLabel} value={row.tldr} disabled={locked} onCommit={(tldr) => void onEdit(row, { tldr })} className="max-w-prose text-control text-fg-muted" />}
        {panels.map((p) => <div key={p.id}>{p.render?.({ id: row.id })}</div>)}
      </header>
      {needsAccess && (
        <p id={ACCESS_NOTE} role="status" className="text-xs text-fg-muted">
          {TEXT.needsAccess}. {TEXT.needsAccessHint} <button type="button" className="underline" onClick={onRetryAccess}>{TEXT.retry}</button>
        </p>
      )}
      <div className="flex flex-col gap-2">
        <div className="flex flex-wrap items-end gap-x-7 gap-y-3">
          {stat(TEXT.statDone, done, 'text-fg')}
          {stat(TEXT.statDoing, doing, 'text-fg')}
          {review > 0 && stat(TEXT.statReview, review, 'text-fg')}
          {stat(TEXT.statBlocked, row.blocked_count, row.blocked_count > 0 ? 'text-danger' : undefined)}
          {stat(TEXT.statTodo, todo)}
          {stat(TEXT.statOf, total, 'text-fg-muted')}
          {momentum && (
            <div className="ml-auto flex flex-col items-end gap-1 text-right">
              <span className={CAPTION}>{TEXT.momentum}</span>
              <button type="button" aria-expanded={why} onClick={() => setWhy((v) => !v)} className="text-control font-semibold text-fg">
                <span aria-hidden>{MOMENTUM_GLYPH[momentum.label]}</span> {TEXT.momentumLabels[momentum.label]}
              </button>
            </div>
          )}
        </div>
        <SegmentedProgress size="lg" items={seg.items} label={seg.label} />
        {why && momentum && <p className="text-xs text-fg-muted">{momentum.text}</p>}
        <p className="sr-only">{seg.label}{momentum ? `. ${TEXT.momentum}: ${TEXT.momentumLabels[momentum.label]}. ${momentum.text}` : ''}</p>
      </div>
      {view === 'board' ? <TaskBoard themeId={row.id} tick={tick} paused={locked} onNeedsAccess={onNeedsAccess} accessNoteId={needsAccess ? ACCESS_NOTE : undefined} /> : <ThemeTimelineView state={timeline} />}
    </section>
  )
}

function Mention({ children }: { children: string }) {
  const text = children.startsWith('@') ? children : `@${children}`
  return <span className="max-w-60 truncate rounded-sm bg-info/12 px-1 text-fg" title={children}>{text}</span>
}

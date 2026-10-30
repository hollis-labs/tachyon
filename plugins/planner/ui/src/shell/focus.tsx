import { DashboardViewTabs } from '@hollis-labs/kit-dashboard/layout'
import { StatusBadge } from '@hollis-labs/kit-dashboard'
import { themeRowActions } from '@planner/components/planner/theme-board'
import { statusToneKey, themeOwners } from '@planner/lib/planner'
import { TEXT } from '@planner/lib/vocabulary'
import { useContributions } from '@planner/lib/regions'
import { SegmentedProgress, StatusMenu } from '@planner/shared'
import { ThemeTimelineView, useThemeTimeline } from '@planner/shell/timeline'
import { TaskBoard } from '@planner/shell/task-board'
import { themeSegments } from '@planner/shell/spine'
import type { ThemeAction, ThemeBoard, ThemeRow } from '@planner/api/types'

const ACCESS_NOTE = 'planner-access-note'

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
  onBack: () => void
  onDetails: () => void
}

export function Focus({ tick, paused, needsAccess, onRetryAccess, onNeedsAccess, row, board, view, onView, onAction, onBack, onDetails }: FocusProps) {
  const seg = themeSegments(row)
  const panels = useContributions('planner.focus.panel')
  const approved = (board.totals.in_flight ?? 0) + (board.totals.cleared ?? 0)
  const actions = themeRowActions(row.section, row.rank, approved)
  const owners = themeOwners(row)
  const timeline = useThemeTimeline(row.id, tick)
  const momentum = timeline.data?.momentum
  return (
    <section aria-labelledby="focus-title" className="flex min-h-0 min-w-0 flex-1 flex-col overflow-y-auto">
      <header className="flex flex-col gap-3 border-b border-border px-4 py-3">
        <div className="flex items-center gap-2">
          <button type="button" onClick={onBack} className="dash-caption text-fg-muted hover:text-fg lg:hidden">← {TEXT.back}</button>
          <span className="rounded-md border border-border px-1.5 font-mono dash-caption text-fg-muted">{row.id}</span>
          <span className="flex-1" />
          <button type="button" onClick={onDetails} className="dash-caption text-fg-muted hover:text-fg xl:hidden">{TEXT.detailsLabel}</button>
        </div>
        <div className="flex flex-wrap items-center gap-3">
          <h1 id="focus-title" className="min-w-0 flex-1 text-lg font-medium text-fg">{row.title}</h1>
          <StatusMenu
            trigger={<StatusBadge status={row.status} tone={statusToneKey(row.status)} />}
            triggerLabel={`${row.status}. ${TEXT.moveTo}`}
            heading={TEXT.moveTo}
            current=""
            options={actions.map((a) => ({ value: a.label, label: a.label }))}
            onSelect={(label) => {
              const a = actions.find((x) => x.label === label)
              if (a) void onAction(row, a.action, a.position === undefined ? undefined : { position: a.position })
            }}
            disabled={actions.length === 0 || paused || needsAccess}
            disabledReasonId={needsAccess ? ACCESS_NOTE : undefined}
          />
        </div>
        {owners && <p className="dash-support text-fg-muted">{owners}</p>}
        {row.tldr && <p className="dash-support text-fg">{row.tldr}</p>}
        <div className="flex flex-col gap-1.5">
          <SegmentedProgress items={seg.items} label={seg.label} />
          <p className="flex flex-wrap gap-x-4 dash-caption text-fg-muted">
            <span>{seg.label}</span>
            {momentum && <span>{TEXT.momentum}: {TEXT.momentumLabels[momentum.label]}. {momentum.text}</span>}
          </p>
        </div>
        {panels.map((p) => <div key={p.id}>{p.render?.({ id: row.id })}</div>)}
        <DashboardViewTabs variant="segments" label={TEXT.viewLabel} value={view} onChange={(v) => onView(v === 'timeline' ? 'timeline' : 'board')} items={[{ value: 'board', label: TEXT.board }, { value: 'timeline', label: TEXT.timeline }]} />
      </header>
      {needsAccess && (
        <p id={ACCESS_NOTE} role="status" className="border-b border-border px-4 py-2 dash-caption text-fg-muted">
          {TEXT.needsAccess}. {TEXT.needsAccessHint} <button type="button" className="underline" onClick={onRetryAccess}>{TEXT.retry}</button>
        </p>
      )}
      <div className="min-w-0 flex-1 p-4">
        {view === 'board' ? <TaskBoard themeId={row.id} tick={tick} paused={paused || needsAccess} onNeedsAccess={onNeedsAccess} accessNoteId={needsAccess ? ACCESS_NOTE : undefined} /> : <ThemeTimelineView state={timeline} />}
      </div>
    </section>
  )
}

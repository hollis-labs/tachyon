import { useMemo, useState } from 'react'
import { toast } from 'sonner'
import { DashboardViewTabs } from '@hollis-labs/kit-dashboard/layout'
import { StatusBadge } from '@hollis-labs/kit-dashboard'
import { themeRowActions } from '@planner/components/planner/theme-board'
import { statusToneKey, themeOwners } from '@planner/lib/planner'
import { FACETS, LANES, TEXT, type LaneId } from '@planner/lib/vocabulary'
import { useContributions } from '@planner/lib/regions'
import { fixtureEvents, fixtureMilestones, fixtureMomentum, fixtureTasks } from '@planner/fixtures/themes'
import { EventTimeline, LaneBoard, SegmentedProgress, StatusMenu, type Lane } from '@planner/shared'
import { themeSegments } from '@planner/shell/spine'
import type { ThemeAction, ThemeBoard, ThemeRow } from '@planner/api/types'

interface FocusProps {
  row: ThemeRow
  board: ThemeBoard
  view: 'board' | 'timeline'
  onView: (v: 'board' | 'timeline') => void
  onAction: (row: ThemeRow, action: ThemeAction, args?: { position?: number }) => Promise<void>
  onBack: () => void
  onDetails: () => void
}

export function Focus({ row, board, view, onView, onAction, onBack, onDetails }: FocusProps) {
  const seg = themeSegments(row)
  const panels = useContributions('planner.focus.panel')
  const approved = (board.totals.in_flight ?? 0) + (board.totals.cleared ?? 0)
  const actions = themeRowActions(row.section, row.rank, approved)
  const owners = themeOwners(row)
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
            disabled={actions.length === 0}
          />
        </div>
        {owners && <p className="dash-support text-fg-muted">{owners}</p>}
        {row.tldr && <p className="dash-support text-fg">{row.tldr}</p>}
        <div className="flex flex-col gap-1.5">
          <SegmentedProgress items={seg.items} label={seg.label} />
          <p className="flex flex-wrap gap-x-4 dash-caption text-fg-muted">
            <span>{seg.label}</span>
            <span title={fixtureMomentum.detail}>{TEXT.momentum}: {fixtureMomentum.label}</span>
            <span className="text-fg-faint">{TEXT.fixtureNote}</span>
          </p>
        </div>
        {panels.map((p) => <div key={p.id}>{p.render?.({ id: row.id })}</div>)}
        <DashboardViewTabs variant="segments" label={TEXT.viewLabel} value={view} onChange={(v) => onView(v === 'timeline' ? 'timeline' : 'board')} items={[{ value: 'board', label: TEXT.board }, { value: 'timeline', label: TEXT.timeline }]} />
      </header>
      <div className="min-w-0 flex-1 p-4">
        {view === 'board' ? <TaskBoard themeId={row.id} /> : <ThemeTimeline />}
      </div>
    </section>
  )
}

function TaskBoard({ themeId }: { themeId: string }) {
  const [tasks, setTasks] = useState(() => fixtureTasks(themeId))
  const byId = useMemo(() => new Map(tasks.map((t) => [t.id, t])), [tasks])
  const lanes: Lane[] = LANES.map((l) => ({
    id: l.id,
    title: l.title,
    collapsed: l.id === 'done',
    cards: tasks.filter((t) => t.lane === l.id).map((t) => ({ id: t.id, content: <span className="dash-support text-fg">{t.title}{t.pr ? <span className="ml-2 font-mono dash-caption text-fg-muted">PR {t.pr}</span> : null}</span> })),
  }))
  // Optimistic: the card moves at once, the write settles after, Undo for 8s.
  // The sample board has no write behind it yet, so nothing is left pending.
  const move = (id: string, from: string, to: string) => {
    const t = byId.get(id)
    if (!t) return
    setTasks((all) => all.map((x) => (x.id === id ? { ...x, lane: to as LaneId } : x)))
    toast(TEXT.moved(t.title, LANES.find((l) => l.id === to)?.title ?? to), {
      duration: 8000,
      action: { label: TEXT.undo, onClick: () => setTasks((all) => all.map((x) => (x.id === id ? { ...x, lane: from as LaneId } : x))) },
    })
  }
  return (
    <>
      <LaneBoard
        lanes={lanes}
        cardTitle={(id) => byId.get(id)?.title ?? id}
        onMove={move}
        labels={{ lane: TEXT.lane, empty: TEXT.laneEmpty, pickedUp: TEXT.pickedUp, movedOver: TEXT.movedOver, dropped: TEXT.dropped, cancelled: TEXT.cancelled, pending: TEXT.saving, dragHint: TEXT.dragHint }}
      />
    </>
  )
}

function ThemeTimeline() {
  const sources = useContributions('planner.focus.timeline.source')
  const events = [...fixtureEvents, ...sources.flatMap((s) => (Array.isArray(s.data) ? s.data : []))]
  const facets = FACETS.map((f) => ({ id: f.id, label: f.label, count: f.id === 'all' ? events.length : events.filter((e) => e.facet === f.id).length }))
  return (
    <div className="flex flex-col gap-4">
      <ol aria-label={TEXT.milestones} className="flex flex-wrap gap-x-6 gap-y-1">
        {fixtureMilestones.map((m) => (
          <li key={m.id} className="dash-caption text-fg-muted">
            <span className="text-fg">{m.label}</span> · {m.state}
          </li>
        ))}
      </ol>
      <EventTimeline events={events} facets={facets} allFacet="all" facetsLabel={TEXT.timelineFacets} streamLabel={TEXT.timelineStream} emptyLabel={TEXT.timelineEmpty} filteredEmptyLabel={TEXT.timelineFilteredEmpty} />
    </div>
  )
}

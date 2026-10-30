import { useEffect, useState } from 'react'
import { cn, Skeleton } from '@hollis-labs/design-components'
import { usePlannerApi } from '@planner/api/context'
import { LANES, TEXT, type LaneId } from '@planner/lib/vocabulary'
import { CAPTION, LaneBoard, SegmentedProgress, type Lane } from '@planner/shared'
import type { PlannerItem, PlannerWorkstreamTasks } from '@planner/api/types'

export type Write = (fn: () => Promise<unknown>) => Promise<void>

export function StreamBody({ item }: { item: PlannerItem }) {
  const api = usePlannerApi()
  const [t, setT] = useState<PlannerWorkstreamTasks | null>(null)
  useEffect(() => {
    let live = true
    setT(null)
    api.plannerWorkstreamTasks(item.id).then((r) => live && setT(r), () => live && setT(null))
    return () => { live = false }
  }, [api, item.id])
  if (!t) return <Skeleton className="h-16 w-full" />
  const c = t.counts
  const done = c.done ?? 0
  const blocked = c.blocked ?? 0
  const doing = c.doing ?? 0
  const review = c.review ?? 0
  const todo = Math.max(0, t.total - done - doing - review - blocked)
  const label = TEXT.progressText(done, t.total, blocked)
  const stat = (name: string, n: number, tone?: string) => (
    <div className="flex flex-col gap-1">
      <span className={CAPTION}>{name}</span>
      <b className={cn('font-mono text-xl font-semibold leading-none tabular-nums', tone ?? 'text-fg-secondary')}>{n}</b>
    </div>
  )
  const lane = (id: LaneId): Lane => ({
    id,
    title: LANES.find((l) => l.id === id)?.title ?? id,
    state: id,
    cards: t.open.filter((o) => o.status === id).map((o) => ({
      id: o.id,
      content: (
        <>
          <span className="truncate font-mono text-caption text-fg-muted">{o.id}</span>
          <span className="text-label text-fg">{o.title}</span>
        </>
      ),
    })),
    limit: 5,
    emptyText: id === 'blocked' ? TEXT.laneEmptyBlocked : id === 'review' ? TEXT.laneEmptyReview : undefined,
  })
  const lanes = [lane('todo'), lane('doing'), lane('review'), lane('blocked')]
  return (
    <div className="flex flex-col gap-5">
      <div className="flex flex-col gap-2">
        <div className="flex flex-wrap items-end gap-x-7 gap-y-3">
          {stat(TEXT.statDone, done, 'text-fg')}
          {stat(TEXT.statDoing, doing, 'text-fg')}
          {review > 0 && stat(TEXT.statReview, review, 'text-fg')}
          {stat(TEXT.statBlocked, blocked, blocked > 0 ? 'text-danger' : undefined)}
          {stat(TEXT.statTodo, todo)}
          {stat(TEXT.statOf, t.total, 'text-fg-muted')}
          <div className="ml-auto flex flex-col items-end gap-1 text-right">
            <span className={CAPTION}>{TEXT.streamSource}</span>
            <span className="text-control font-semibold text-fg">{t.torque_project_ids.length > 0 ? `${TEXT.streamProject} ${t.torque_project_ids.join(', ')}` : TEXT.streamNote}{t.torque_tag ? ` · ${TEXT.streamTag} ${t.torque_tag}` : ''}</span>
          </div>
        </div>
        <SegmentedProgress size="lg" label={label} items={[{ state: 'done', count: done }, { state: 'doing', count: doing }, { state: 'review', count: review }, { state: 'blocked', count: blocked }, { state: 'todo', count: todo }]} />
      </div>
      {t.open.length === 0 && t.total === 0 ? <p className="text-control text-fg-muted">{TEXT.streamNoTasks}</p> : (
        <LaneBoard
          readOnly
          lanes={lanes}
          cardTitle={(id) => t.open.find((o) => o.id === id)?.title ?? id}
          onMove={() => undefined}
          labels={{ lane: TEXT.lane, empty: TEXT.laneEmpty, pickedUp: TEXT.pickedUp, movedOver: TEXT.movedOver, dropped: TEXT.dropped, cancelled: TEXT.cancelled, pending: TEXT.saving, dragHint: TEXT.dragHint, more: TEXT.laneMore, collapsed: TEXT.laneCollapsed, wasHere: TEXT.wasHere }}
        />
      )}
    </div>
  )
}

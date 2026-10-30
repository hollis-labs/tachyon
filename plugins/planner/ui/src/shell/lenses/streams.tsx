import { useEffect, useState } from 'react'
import { Button, Skeleton, Textarea } from '@hollis-labs/design-components'
import { DashboardViewTabs } from '@hollis-labs/kit-dashboard/layout'
import { createPlannerId } from '@planner/api/identity'
import { usePlannerApi } from '@planner/api/context'
import { notifyError, notifySuccess } from '@planner/api/notifications'
import { TEXT, TRIAGE_ROUTES } from '@planner/lib/vocabulary'
import { OptionCompare, SegmentedProgress, TriageStack } from '@planner/shared'
import type { IdeaPromoteTarget, PlannerDb, PlannerItem, PlannerWorkstreamTasks } from '@planner/api/types'

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
  const label = TEXT.progressText(done, t.total, blocked)
  return (
    <div className="flex flex-col gap-3">
      <SegmentedProgress label={label} items={[{ state: 'done', count: done }, { state: 'doing', count: doing }, { state: 'review', count: review }, { state: 'blocked', count: blocked }, { state: 'todo', count: Math.max(0, t.total - done - doing - review - blocked) }]} />
      <p className="dash-caption text-fg-muted">{label} · {TEXT.streamNote}</p>
      <h2 className="font-mono dash-caption uppercase tracking-label text-fg-faint">{TEXT.streamTasks}</h2>
      {t.open.length === 0 ? <p className="dash-support text-fg-muted">{TEXT.streamNoTasks}</p> : (
        <ul className="flex flex-col gap-1">{t.open.map((o) => <li key={o.id} className="dash-support text-fg"><span className="mr-2 font-mono dash-caption text-fg-faint">{o.id}</span>{o.title}<span className="ml-2 dash-caption text-fg-muted">{o.status}</span></li>)}</ul>
      )}
    </div>
  )
}


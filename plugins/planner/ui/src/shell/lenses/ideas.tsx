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

const PROMOTE_TARGETS: { to: IdeaPromoteTarget; label: string }[] = [
  { to: 'themes', label: 'Theme' },
  { to: 'roadmap', label: 'Roadmap item' },
  { to: 'decisions', label: 'Decision' },
]

export function IdeaBody({ item, write }: { item: PlannerItem; write: Write }) {
  const api = usePlannerApi()
  const [notes, setNotes] = useState(item.notes ?? '')
  useEffect(() => setNotes(item.notes ?? ''), [item.id, item.rev])
  // One target id per (idea, target), so a retry after a failure never creates twice.
  const [targets] = useState(() => new Map<string, string>())
  const promote = (to: IdeaPromoteTarget) => write(async () => {
    const key = `${item.id}:${to}`
    const targetId = targets.get(key) ?? createPlannerId()
    targets.set(key, targetId)
    const res = await api.plannerIdeaPromote(item.id, to, item.rev, targetId)
    notifySuccess(TEXT.promoted(res.created.id))
  })
  const save = () => notes !== (item.notes ?? '') && void write(() => api.plannerUpdate('ideas', item.id, { notes }, item.rev))
  return (
    <div className="flex flex-col gap-3">
      <label className="font-mono dash-caption uppercase tracking-label text-fg-faint" htmlFor="idea-notes">{TEXT.notes}</label>
      <Textarea id="idea-notes" rows={10} value={notes} onChange={(e) => setNotes(e.target.value)} onBlur={save} onKeyDown={(e) => { if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) { e.preventDefault(); save() } }} />
      <div role="group" aria-label={TEXT.promoteTo} className="flex flex-wrap items-center gap-2 dash-caption text-fg-faint">
        {TEXT.promoteTo}:
        {PROMOTE_TARGETS.map((t) => (
          <Button key={t.to} size="sm" variant="outline" onClick={() => void promote(t.to)}>{t.label}</Button>
        ))}
      </div>
    </div>
  )
}


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

export function RoadmapBody({ item, horizons, write }: { item: PlannerItem; horizons: string[]; write: Write }) {
  const api = usePlannerApi()
  const current = typeof item.horizon === 'string' ? item.horizon : ''
  if (horizons.length === 0) return null
  return (
    <div className="flex flex-col gap-2">
      <h2 className="font-mono dash-caption uppercase tracking-label text-fg-faint">{TEXT.horizon}</h2>
      <DashboardViewTabs variant="segments" label={TEXT.horizonLabel} value={current} items={horizons.map((h) => ({ value: h, label: h }))} onChange={(horizon) => void write(() => api.plannerUpdate('roadmap', item.id, { horizon }, item.rev))} />
    </div>
  )
}


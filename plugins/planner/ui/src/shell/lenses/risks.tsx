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

export function RiskBody({ item, levels }: { item: PlannerItem; levels: string[] }) {
  const sev = typeof item.severity === 'string' ? item.severity : ''
  const at = Math.max(0, levels.indexOf(sev))
  return (
    <div className="flex flex-col gap-2">
      <p className="flex items-center gap-2 dash-support text-fg">
        <span className="font-mono dash-caption uppercase tracking-label text-fg-faint">{TEXT.severity}</span>
        <span aria-hidden className="flex gap-1">{levels.map((l, i) => <span key={l} className={`size-2 rounded-full ${i <= at && sev ? 'bg-fg' : 'bg-surface-hover'}`} />)}</span>
        {sev}
      </p>
      {(item.related_ids ?? []).length > 0 && <p className="dash-caption text-fg-muted">{TEXT.mitigatedBy}: {item.related_ids!.join(', ')}</p>}
    </div>
  )
}


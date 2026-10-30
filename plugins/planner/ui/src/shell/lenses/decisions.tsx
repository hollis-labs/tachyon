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

export function DecisionBody({ item, write, busy }: { item: PlannerItem; write: Write; busy: boolean }) {
  const api = usePlannerApi()
  const options = item.options ?? []
  const chosen = typeof item.chosen === 'string' ? item.chosen : typeof item.decision === 'string' ? item.decision : item.status === 'decided' ? '' : undefined
  if (options.length === 0) return <p className="dash-support text-fg-muted">{TEXT.noOptions}</p>
  return <OptionCompare options={options} chosenId={chosen || undefined} onChoose={(opt) => void write(() => api.plannerDecide(item.id, opt, item.rev))} busy={busy} labels={{ choose: TEXT.chooseOption, chooseButton: TEXT.chooseButton, recommended: TEXT.recommended, chosen: TEXT.chosen, pros: TEXT.pros, cons: TEXT.cons, groupLabel: TEXT.optionsGroup }} />
}


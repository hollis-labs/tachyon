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

export function InboxFocus({ items, loading, onChanged }: { items: PlannerItem[]; loading: boolean; onChanged: () => Promise<void> }) {
  const api = usePlannerApi()
  const [busy, setBusy] = useState(false)
  const [targets] = useState(() => new Map<string, string>()) // one target id per (card, route), so a retry never creates twice
  const card = items[0]
  const run = async (fn: () => Promise<unknown>) => {
    setBusy(true)
    try {
      await fn()
    } catch (err) {
      notifyError(err, TEXT.writeFailed)
    } finally {
      setBusy(false)
      await onChanged()
    }
  }
  if (loading) return <Skeleton className="m-4 h-32" />
  return (
    <div className="p-4">
      <TriageStack
        card={card ? <div className="flex flex-col gap-1"><h1 className="dash-support font-medium text-fg">{card.title}</h1>{card.tldr && <p className="dash-support text-fg-muted">{card.tldr}</p>}</div> : null}
        remaining={items.length}
        nextTitle={items[1]?.title}
        routes={TRIAGE_ROUTES}
        busy={busy}
        onRoute={(to) => card && void run(async () => {
          const key = `${card.id}:${to}`
          const targetId = targets.get(key) ?? createPlannerId()
          targets.set(key, targetId)
          const res = await api.plannerInboxPromote(card.id, to as PlannerDb, card.rev, targetId)
          notifySuccess(TEXT.promoted(res.item.id))
        })}
        onDismiss={() => card && void run(() => api.plannerInboxDismiss(card.id, card.rev))}
        labels={{ count: TEXT.triageCount, allTriaged: TEXT.allTriaged, routeHeading: TEXT.triageInto, dismiss: TEXT.dismiss, next: TEXT.nextUp }}
      />
    </div>
  )
}

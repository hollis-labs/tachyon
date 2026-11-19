import { useState } from 'react'
import { Skeleton } from '@hollis-labs/design-components'
import { createPlannerId } from '@planner/api/identity'
import { usePlannerApi } from '@planner/api/context'
import { notifyError, notifySuccess } from '@planner/api/notifications'
import { boardAge } from '@planner/lib/planner'
import { TEXT, TRIAGE_ROUTES } from '@planner/lib/vocabulary'
import { Chip, TriageStack } from '@planner/shared'
import type { PlannerDb, PlannerItem } from '@planner/api/types'

export type Write = (fn: () => Promise<unknown>) => Promise<void>

export function InboxFocus({ items, selectedId, loading, onChanged }: { items: PlannerItem[]; selectedId?: string; loading: boolean; onChanged: () => Promise<void> }) {
  const api = usePlannerApi()
  const [busy, setBusy] = useState(false)
  const [targets] = useState(() => new Map<string, string>()) // one target id per (card, route), so a retry never creates twice
  const at = Math.max(0, items.findIndex((i) => i.id === selectedId))
  const card = items[at]
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
  const by = typeof card?.added_by === 'string' ? card.added_by : ''
  const age = card && typeof card.created === 'string' ? boardAge(card.created, Date.now()) : ''
  return (
    <section aria-label={TEXT.inboxLabel} className="flex min-h-0 min-w-0 flex-1 flex-col overflow-y-auto px-6 py-5 max-sm:px-4 max-sm:py-4">
      <TriageStack
        card={card ? (
          <div className="flex flex-col gap-2.5">
            <div className="flex flex-wrap items-center gap-2">
              <Chip square title={card.id}>{typeof card.legacy_id === 'string' ? card.legacy_id : card.id.slice(0, 8)}</Chip>
              {typeof card.kind === 'string' && <Chip square>{card.kind}</Chip>}
              {(by || age) && <span className="text-label text-fg-muted">{[by && TEXT.capturedBy(by), age].filter(Boolean).join(' · ')}</span>}
            </div>
            <h1 className="font-sans text-xl font-semibold normal-case leading-tight tracking-normal text-fg">{card.title}</h1>
            {card.tldr && <p className="text-control text-fg-muted">{card.tldr}</p>}
          </div>
        ) : null}
        index={at}
        remaining={items.length}
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
        labels={{ heading: TEXT.inboxHeading, position: TEXT.triagePosition, allTriaged: TEXT.allTriaged, routeHeading: TEXT.triageInto, dismiss: TEXT.dismiss }}
      />
    </section>
  )
}

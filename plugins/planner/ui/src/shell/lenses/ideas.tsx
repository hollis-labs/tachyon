import { useEffect, useState } from 'react'
import { Textarea } from '@hollis-labs/design-components'
import { createPlannerId } from '@planner/api/identity'
import { usePlannerApi } from '@planner/api/context'
import { notifySuccess } from '@planner/api/notifications'
import { TEXT } from '@planner/lib/vocabulary'
import { CAPTION, Kbd } from '@planner/shared'
import type { IdeaPromoteTarget, PlannerItem } from '@planner/api/types'

export type Write = (fn: () => Promise<unknown>) => Promise<void>

const PROMOTE_TARGETS: { to: IdeaPromoteTarget; label: string; key: string }[] = [
  { to: 'themes', label: 'Theme', key: 'T' },
  { to: 'roadmap', label: 'Roadmap item', key: 'R' },
  { to: 'decisions', label: 'Decision', key: 'D' },
]

/** The idea's writing surface and the three places it can go. */
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
    <>
      <Textarea
        aria-label={TEXT.notes}
        placeholder={TEXT.ideaWriting}
        rows={8}
        value={notes}
        onChange={(e) => setNotes(e.target.value)}
        onBlur={save}
        onKeyDown={(e) => { if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) { e.preventDefault(); save() } }}
        className="min-h-40 resize-y rounded-panel border border-border bg-transparent px-4 py-3 text-control text-fg"
      />
      <section aria-label={TEXT.promoteTo} className="flex flex-col gap-2">
        <h2 className={CAPTION}>{TEXT.promoteTo}</h2>
        <div role="group" aria-label={TEXT.promoteTo} className="grid grid-cols-3 gap-2 max-sm:grid-cols-1" onKeyDown={(e) => {
          const t = PROMOTE_TARGETS.find((x) => x.key.toLowerCase() === e.key.toLowerCase())
          if (t && !e.metaKey && !e.ctrlKey && !e.altKey) void promote(t.to)
        }}>
          {PROMOTE_TARGETS.map((t) => (
            <button key={t.to} type="button" aria-label={t.label} onClick={() => void promote(t.to)} className="flex h-9 items-center justify-between rounded-panel border border-border px-3 text-control text-fg hover:bg-surface-hover">
              {t.label}
              <Kbd>{t.key}</Kbd>
            </button>
          ))}
        </div>
      </section>
    </>
  )
}

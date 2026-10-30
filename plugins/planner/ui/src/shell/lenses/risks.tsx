import { useEffect, useState } from 'react'
import { usePlannerApi } from '@planner/api/context'
import { sentenceCase } from '@planner/lib/planner'
import { TEXT } from '@planner/lib/vocabulary'
import { CAPTION, SegmentedSwitch } from '@planner/shared'
import type { PlannerItem } from '@planner/api/types'

export type Write = (fn: () => Promise<unknown>) => Promise<void>

/** A risk's status as a stepper, and the themes that mitigate it. */
export function RiskBody({ item, statuses, write }: { item: PlannerItem; statuses: string[]; write: Write }) {
  const api = usePlannerApi()
  const [themes, setThemes] = useState<{ id: string; title: string }[] | null>(null)
  useEffect(() => {
    let live = true
    setThemes(null)
    api.plannerLinks('risks', item.id).then((l) => live && setThemes(l.outbound.filter((o) => o.to.db === 'themes').map((o) => ({ id: o.to.id, title: o.title }))), () => live && setThemes([]))
    return () => { live = false }
  }, [api, item.id])
  return (
    <>
      {statuses.length > 0 && (
        <section aria-label={TEXT.statusLabel} className="flex flex-col gap-2">
          <h2 className={CAPTION}>{TEXT.statusLabel}</h2>
          <div>
            <SegmentedSwitch label={TEXT.statusLabel} value={item.status ?? ''} items={statuses.map((s) => ({ value: s, label: sentenceCase(s) }))} onChange={(status) => status !== item.status && void write(() => api.plannerUpdate('risks', item.id, { status }, item.rev))} />
          </div>
        </section>
      )}
      <section aria-label={TEXT.mitigatedBy} className="flex flex-col gap-2">
        <h2 className={CAPTION}>{TEXT.mitigatedBy}</h2>
        {themes === null ? null : themes.length === 0 ? (
          <p className="text-control text-fg-muted">{TEXT.mitigatedByNone}</p>
        ) : (
          <ul className="grid grid-cols-2 gap-2 max-sm:grid-cols-1">
            {themes.map((t) => (
              <li key={t.id} className="flex flex-col gap-1 rounded-panel border border-border bg-bg-elevated px-3 py-2">
                <span className="font-mono text-caption text-fg-muted">{t.id.slice(0, 8)} · {TEXT.themeNoun}</span>
                <a href={`/planner/themes/${encodeURIComponent(t.id)}`} className="text-control text-fg hover:underline">{t.title}</a>
              </li>
            ))}
          </ul>
        )}
      </section>
    </>
  )
}

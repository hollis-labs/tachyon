import { useEffect, useState } from 'react'
import { cn } from '@hollis-labs/design-components'
import { usePlannerApi } from '@planner/api/context'
import { TEXT } from '@planner/lib/vocabulary'
import { CAPTION, Chip } from '@planner/shared'
import type { PlannerItem } from '@planner/api/types'

export type Write = (fn: () => Promise<unknown>) => Promise<void>

/**
 * Where an item sits on the horizon, and which themes deliver it. The horizon is one track of cells: the
 * item's own cell holds its chip, the others count their items; a click (or 1 / 2 / 3) moves it there.
 */
export function RoadmapBody({ item, horizons, siblings, write }: { item: PlannerItem; horizons: string[]; siblings: readonly PlannerItem[]; write: Write }) {
  const api = usePlannerApi()
  const current = typeof item.horizon === 'string' ? item.horizon : ''
  const [themes, setThemes] = useState<{ id: string; title: string; status?: string }[] | null>(null)
  useEffect(() => {
    let live = true
    setThemes(null)
    api.plannerLinks('roadmap', item.id).then((l) => live && setThemes(l.outbound.filter((o) => o.to.db === 'themes').map((o) => ({ id: o.to.id, title: o.title, status: o.status }))), () => live && setThemes([]))
    return () => { live = false }
  }, [api, item.id])
  const move = (horizon: string) => horizon !== current && void write(() => api.plannerUpdate('roadmap', item.id, { horizon }, item.rev))
  const live = (i: PlannerItem) => i.status !== 'completed' && i.status !== 'cancelled'
  const count = (h: string) => siblings.filter((i) => live(i) && i.horizon === h && i.id !== item.id).length
  return (
    <>
      {horizons.length > 0 && (
        <section aria-label={TEXT.horizonLabel} className="flex flex-col gap-2">
          <h2 className={CAPTION}>{TEXT.horizonHint(horizons.length)}</h2>
          <div role="radiogroup" aria-label={TEXT.horizonLabel} className="grid overflow-hidden rounded-panel border border-border" style={{ gridTemplateColumns: `repeat(${horizons.length}, minmax(0, 1fr))` }}>
            {horizons.map((h, i) => {
              const here = h === current
              const n = count(h)
              return (
                <button
                  key={h}
                  type="button"
                  role="radio"
                  aria-checked={here}
                  onClick={() => move(h)}
                  onKeyDown={(e) => { if (e.key === String(i + 1)) move(h) }}
                  className={cn('flex min-h-20 flex-col items-start gap-1.5 border-border p-3 text-left not-first:border-l hover:bg-surface-hover max-sm:p-2', here && 'bg-selection')}
                >
                  <span className={CAPTION}>{h}</span>
                  {here ? (
                    <>
                      <Chip square className="max-w-full truncate">{item.title}</Chip>
                      {n > 0 && <span className="text-control text-fg-secondary">{TEXT.withOthers(n)}</span>}
                    </>
                  ) : (
                    <span className="text-control text-fg-secondary">{TEXT.itemCount(n)}</span>
                  )}
                </button>
              )
            })}
          </div>
        </section>
      )}
      <section aria-label={TEXT.deliveredBy} className="flex flex-col gap-2">
        <h2 className={CAPTION}>{TEXT.deliveredBy}</h2>
        {themes === null ? null : themes.length === 0 ? (
          <p className="text-control text-fg-muted">{TEXT.deliveredByNone}</p>
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

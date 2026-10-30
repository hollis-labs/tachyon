import { useMemo } from 'react'
import { DashboardViewTabs } from '@hollis-labs/kit-dashboard/layout'
import { LinkPort, LinkTag, SegmentedProgress, SpineList, type SegmentedProgressItem, type SpineGroup } from '@planner/shared'
import { THEME_SECTIONS, themeWaiting } from '@planner/lib/planner'
import { LENSES, TEXT } from '@planner/lib/vocabulary'
import { useContributions } from '@planner/lib/regions'
import type { PlannerDb, ThemeBoard, ThemeRow } from '@planner/api/types'

/** Progress items for a theme: the server's counts by status, with blocked taken from blocked_count. */
export function themeSegments(row: ThemeRow): { items: SegmentedProgressItem[]; label: string } {
  const c = row.progress.counts ?? {}
  const total = row.progress.total ?? 0
  const done = row.progress.done ?? c.done ?? 0
  const blocked = row.blocked_count
  const doing = c.doing ?? c.in_progress ?? 0
  const review = c.review ?? 0
  const todo = Math.max(0, total - done - doing - review - blocked)
  return {
    items: [{ state: 'done', count: done }, { state: 'doing', count: doing }, { state: 'review', count: review }, { state: 'blocked', count: blocked }, { state: 'todo', count: todo }],
    label: total ? TEXT.progressText(done, total, blocked) : TEXT.noTasks,
  }
}

/** Provisional: a theme needs the operator when something is waiting on a person (pending decisions, unmet dependencies) or it is blocked. */
export const needsYou = (row: ThemeRow) => row.blocked_count > 0 || themeWaiting(row).length > 0

export function rowsOf(board: ThemeBoard | null): ThemeRow[] {
  return board ? THEME_SECTIONS.flatMap((s) => board.sections[s.key] ?? []) : []
}

interface SpineProps {
  board: ThemeBoard | null
  selectedId?: string
  onSelect: (id: string) => void
  onLens: (db: PlannerDb) => void
  pins: ReadonlySet<string>
  onTogglePin: (id: string) => void
  needYouOnly: boolean
  onNeedYou: (on: boolean) => void
  /** Links by item id: relation kind and the other item's title (sample data until the backend writes links). */
  links: ReadonlyMap<string, { kind: string; other: string }>
}

export function Spine({ board, selectedId, onSelect, onLens, pins, onTogglePin, needYouOnly, onNeedYou, links }: SpineProps) {
  const lensExtras = useContributions('planner.lens')
  const itemMenu = useContributions('planner.spine.item.menu')
  const all = useMemo(() => rowsOf(board), [board])
  const needCount = all.filter(needsYou).length
  const groups = useMemo<SpineGroup[]>(() => {
    const pass = (r: ThemeRow) => !needYouOnly || needsYou(r)
    const entry = (r: ThemeRow) => {
      const seg = themeSegments(r)
      return {
        id: r.id,
        title: r.title,
        rank: r.rank,
        pinned: pins.has(r.id),
        progress: <SegmentedProgress items={seg.items} label={seg.label} />,
        port: <LinkPort id={r.id} title={r.title} labels={{ port: TEXT.linkPort, dragging: TEXT.linkDragging }} />,
        flags: links.has(r.id) ? <LinkTag>{TEXT.linked(links.get(r.id)!.kind, links.get(r.id)!.other)}</LinkTag> : undefined,
        menu: itemMenu.map((c) => <span key={c.id}>{c.render?.({ id: r.id })}</span>),
      }
    }
    const pinned = all.filter((r) => pins.has(r.id) && pass(r)).map(entry)
    const rest = THEME_SECTIONS.map((s) => ({ id: s.key, title: s.title, items: (board?.sections[s.key] ?? []).filter((r) => pass(r) && !pins.has(r.id)).map(entry) })).filter((g) => g.items.length > 0)
    return [...(pinned.length ? [{ id: 'pinned', title: TEXT.pinned, items: pinned }] : []), ...rest]
  }, [all, board, itemMenu, links, needYouOnly, pins])

  const items = [...LENSES.map((l) => ({ value: l.db, label: l.label })), ...lensExtras.map((c) => ({ value: c.id, label: c.label ?? c.id }))]
  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div className="overflow-x-auto border-b border-border px-3 py-2">
        <DashboardViewTabs label={TEXT.lensesLabel} value="themes" items={items} onChange={(v) => LENSES.some((l) => l.db === v) && onLens(v as PlannerDb)} />
      </div>
      <div className="flex items-center gap-2 border-b border-border px-3 py-2">
        <button type="button" aria-pressed={needYouOnly} onClick={() => onNeedYou(!needYouOnly)} className="rounded-full border border-border px-2.5 py-0.5 dash-caption text-fg-muted hover:bg-panel-hover aria-pressed:bg-panel-hover aria-pressed:text-fg">
          {TEXT.needYou} <span className="tabular-nums text-fg-faint">{needCount}</span>
        </button>
        {needYouOnly && (
          <button type="button" onClick={() => onNeedYou(false)} className="dash-caption text-fg-faint hover:text-fg">
            {TEXT.clearFilter}
          </button>
        )}
      </div>
      <div className="min-h-0 flex-1 overflow-y-auto">
        <SpineList label={TEXT.spineLabel} groups={groups} selectedId={selectedId} onSelect={onSelect} onTogglePin={onTogglePin} pendingLabel={TEXT.saving} pinnedLabel={TEXT.pinned} emptyLabel={needYouOnly ? TEXT.needYouEmpty : undefined} />
      </div>
    </div>
  )
}

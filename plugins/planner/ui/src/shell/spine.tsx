import { useMemo } from 'react'
import { LinkPort, LinkTag, SegmentedProgress, StateDot, type SegmentedProgressItem, type SpineGroup } from '@planner/shared'
import { THEME_SECTIONS, themeWaiting } from '@planner/lib/planner'
import { TEXT } from '@planner/lib/vocabulary'
import type { Contribution } from '@planner/lib/regions'
import type { ThemeBoard, ThemeRow } from '@planner/api/types'

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

/** Spine entries for one theme: its segmented progress, pin, link port and link tag. */
export function useThemeGroups({ board, pins, needYouOnly, needs, links, itemMenu }: { board: ThemeBoard | null; pins: ReadonlySet<string>; needYouOnly: boolean; needs: (r: ThemeRow) => boolean; links: ReadonlyMap<string, { kind: string; other: string }>; itemMenu: readonly Contribution[] }): SpineGroup[] {
  const all = useMemo(() => rowsOf(board), [board])
  return useMemo<SpineGroup[]>(() => {
    const pass = (r: ThemeRow) => !needYouOnly || needs(r)
    const entry = (r: ThemeRow) => {
      const seg = themeSegments(r)
      return {
        id: r.id,
        title: r.title,
        rank: r.rank,
        pinned: pins.has(r.id),
        flags: needs(r) ? <StateDot state={r.blocked_count > 0 ? 'blocked' : 'review'} className="self-center" /> : undefined,
        progress: <SegmentedProgress size="sm" items={seg.items} label={seg.label} />,
        port: <LinkPort id={r.id} title={r.title} labels={{ port: TEXT.linkPort, dragging: TEXT.linkDragging }} />,
        tag: links.has(r.id) ? <LinkTag>{links.get(r.id)!.kind}</LinkTag> : undefined,
        menu: itemMenu.map((c) => <span key={c.id}>{c.render?.({ id: r.id })}</span>),
      }
    }
    const pinned = all.filter((r) => pins.has(r.id) && pass(r)).map(entry)
    const of = (keys: readonly string[]) => THEME_SECTIONS.filter((s) => keys.includes(s.key)).flatMap((s) => (board?.sections[s.key] ?? []).filter((r) => pass(r) && !pins.has(r.id)).map(entry))
    const live = of(['in_flight']), cleared = of(['cleared']), settled = of(['queued', 'parked', 'landed']), proposed = of(['proposed'])
    const groups: SpineGroup[] = [
      ...(pinned.length ? [{ id: 'pinned', title: TEXT.pinned, items: pinned }] : []),
      { id: 'in_flight', title: TEXT.groupLive, items: live },
      { id: 'cleared', title: TEXT.groupCleared, items: cleared },
      { id: 'settled', title: TEXT.groupSettled, items: settled, collapsed: true },
      { id: 'proposed', title: TEXT.groupProposed, items: proposed, collapsed: true },
    ]
    return groups.filter((g) => g.items.length > 0)
  }, [all, board, itemMenu, links, needYouOnly, needs, pins])
}

import { useRef, type KeyboardEvent, type ReactNode } from 'react'
import { cn } from '@hollis-labs/design-components'

export interface SpineEntry {
  id: string
  title: string
  /** Rank shown before the title, when the list is a sequence. */
  rank?: number
  pinned?: boolean
  /** Progress, usually a SegmentedProgress. */
  progress?: ReactNode
  /** Small flags after the title (text, never colour alone). */
  flags?: ReactNode
  /** Row-level menu slot (plugin contributions land here). */
  menu?: ReactNode
  /** "Saving" while an optimistic write is in flight. */
  pending?: boolean
}

export interface SpineGroup {
  id: string
  title: string
  items: readonly SpineEntry[]
}

export interface SpineListProps {
  groups: readonly SpineGroup[]
  selectedId?: string
  onSelect: (id: string) => void
  /** Called for P on the focused row. */
  onTogglePin?: (id: string) => void
  /** Name of the list for assistive tech. */
  label: string
  pendingLabel?: string
  pinnedLabel?: string
  emptyLabel?: string
  className?: string
}

/**
 * Ranked, grouped list of items with progress. One listbox; J/K or arrows rove
 * focus, Enter opens, P pins. Group titles are headings, not options.
 */
export function SpineList({ groups, selectedId, onSelect, onTogglePin, label, pendingLabel = 'Saving', pinnedLabel = 'Pinned', emptyLabel, className }: SpineListProps) {
  const root = useRef<HTMLDivElement>(null)
  const all = groups.flatMap((g) => g.items)

  const focusAt = (i: number) => {
    const rows = root.current?.querySelectorAll<HTMLElement>('[role="option"]')
    rows?.[Math.max(0, Math.min(i, (rows?.length ?? 1) - 1))]?.focus()
  }
  const onKeyDown = (e: KeyboardEvent<HTMLDivElement>) => {
    if (e.metaKey || e.ctrlKey || e.altKey) return
    const rows = [...(root.current?.querySelectorAll<HTMLElement>('[role="option"]') ?? [])]
    const at = rows.indexOf(document.activeElement as HTMLElement)
    const k = e.key.toLowerCase()
    if (k === 'j' || e.key === 'ArrowDown') (e.preventDefault(), focusAt(at + 1))
    else if (k === 'k' || e.key === 'ArrowUp') (e.preventDefault(), focusAt(at < 0 ? 0 : at - 1))
    else if (k === 'p' && at >= 0 && onTogglePin) (e.preventDefault(), onTogglePin(all[at].id))
  }

  if (all.length === 0 && emptyLabel) return <p className={cn('px-3 py-2 dash-support text-fg-muted', className)}>{emptyLabel}</p>

  return (
    <div ref={root} role="listbox" aria-label={label} onKeyDown={onKeyDown} className={cn('flex min-w-0 flex-col', className)}>
      {groups.map((g) => (
        <div key={g.id} role="group" aria-labelledby={`spine-${g.id}`} className="flex flex-col">
          <h3 id={`spine-${g.id}`} className="flex items-center gap-2 px-3 pb-1 pt-3 font-mono dash-caption uppercase tracking-label text-fg-faint">
            {g.title}
            <span className="tabular-nums">{g.items.length}</span>
          </h3>
          {g.items.map((it) => {
            const selected = it.id === selectedId
            return (
              <div
                key={it.id}
                role="option"
                aria-selected={selected}
                tabIndex={selected || (!selectedId && it.id === all[0]?.id) ? 0 : -1}
                onClick={() => onSelect(it.id)}
                onKeyDown={(e) => {
                  if (e.key === 'Enter' && e.target === e.currentTarget) onSelect(it.id)
                }}
                className={cn('group relative flex cursor-pointer flex-col gap-1.5 border-l-2 border-transparent px-3 py-2 outline-none hover:bg-panel-hover focus-visible:bg-panel-hover', selected && 'border-primary bg-panel-hover')}
              >
                <div className="flex min-w-0 items-center gap-2">
                  {it.rank !== undefined && <span className="font-mono dash-caption tabular-nums text-fg-faint">{it.rank}</span>}
                  <span className="min-w-0 flex-1 truncate dash-support text-fg">{it.title}</span>
                  {it.pinned && <span className="dash-caption text-fg-faint">{pinnedLabel}</span>}
                  {it.pending && <span className="dash-caption text-fg-faint">{pendingLabel}</span>}
                  {it.flags}
                  {it.menu}
                </div>
                {it.progress}
              </div>
            )
          })}
        </div>
      ))}
    </div>
  )
}

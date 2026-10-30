import { useRef, useState, type KeyboardEvent, type ReactNode } from 'react'
import { Pin } from 'lucide-react'
import { cn } from '@hollis-labs/design-components'
import { GroupHeading } from './primitives'

export interface SpineEntry {
  id: string
  title: string
  /** Rank shown before the title, when the list is a sequence. */
  rank?: number
  pinned?: boolean
  /** Before the title: a state dot, for lists without a rank. */
  leading?: ReactNode
  /** A second row under the title: kind chip, area, waiting time. */
  meta?: ReactNode
  /** Progress, usually a SegmentedProgress. */
  progress?: ReactNode
  /** Small flags after the title (text, never colour alone). */
  flags?: ReactNode
  /** Relation tag: drawn on the right edge with a dashed bracket, e.g. a LinkTag. */
  tag?: ReactNode
  /** Right-edge slot for a link port; the row is positioned for it. */
  port?: ReactNode
  /** Row-level menu slot (plugin contributions land here). */
  menu?: ReactNode
  /** "Saving" while an optimistic write is in flight. */
  pending?: boolean
}

export interface SpineGroup {
  id: string
  title: string
  items: readonly SpineEntry[]
  /** Starts folded to its heading and count; the heading opens it. */
  collapsed?: boolean
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
  const [opened, setOpened] = useState<ReadonlySet<string>>(new Set())
  const folded = (g: SpineGroup) => Boolean(g.collapsed) && !opened.has(g.id)
  const all = groups.filter((g) => !folded(g)).flatMap((g) => g.items)
  const toggle = (id: string) => setOpened((o) => { const n = new Set(o); if (!n.delete(id)) n.add(id); return n })

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

  if (all.length === 0 && emptyLabel) return <p className={cn('px-3 py-2 text-xs text-fg-muted', className)}>{emptyLabel}</p>

  return (
    <div ref={root} role="listbox" aria-label={label} onKeyDown={onKeyDown} className={cn('flex min-w-0 flex-col', className)}>
      {groups.map((g) => (
        <div key={g.id} role="group" aria-labelledby={`spine-${g.id}`} className="flex flex-col">
          <GroupHeading id={`spine-${g.id}`} count={g.items.length} onToggle={g.collapsed ? () => toggle(g.id) : undefined} open={!folded(g)}>{g.title}</GroupHeading>
          {!folded(g) && g.items.map((it) => {
            const selected = it.id === selectedId
            return (
              <div
                key={it.id}
                role="option"
                aria-selected={selected}
                aria-current={selected || undefined}
                tabIndex={selected || (!selectedId && it.id === all[0]?.id) ? 0 : -1}
                onClick={() => onSelect(it.id)}
                onKeyDown={(e) => {
                  if (e.key === 'Enter' && e.target === e.currentTarget) onSelect(it.id)
                }}
                className={cn('group relative flex cursor-pointer flex-col gap-1.5 rounded-control border-l-2 border-transparent py-2 pl-2.5 pr-2 outline-none hover:bg-selection focus-visible:outline focus-visible:outline-1 focus-visible:outline-ring', selected && 'border-primary bg-selection')}
              >
                <span aria-hidden className="absolute -left-0.5 top-1/2 grid -translate-y-1/2 grid-cols-2 gap-0.5 opacity-0 group-hover:opacity-80">
                  {[0, 1, 2, 3, 4, 5].map((i) => <i key={i} className="size-0.5 rounded-full bg-fg-muted" />)}
                </span>
                <div className="flex min-w-0 items-baseline gap-2">
                  {it.rank !== undefined && <span className="min-w-3.5 font-mono text-label font-semibold tabular-nums text-fg-muted">{it.rank}</span>}
                  {it.leading}
                  <span className="min-w-0 flex-1 truncate text-control font-medium text-fg">{it.title}</span>
                  {it.pinned && <Pin aria-label={pinnedLabel} className="size-3 shrink-0 self-center text-fg-secondary" />}
                  {it.pending && <span className="text-caption text-fg-faint">{pendingLabel}</span>}
                  {it.flags}
                  {it.menu}
                </div>
                {it.meta}
                {it.progress}
                {it.tag && (
                  <>
                    <span aria-hidden className="pointer-events-none absolute -right-px bottom-5 top-5 w-2 rounded-r-md border border-l-0 border-dashed border-fg-muted" />
                    <span className="absolute right-3 top-1/2 z-10 -translate-y-1/2">{it.tag}</span>
                  </>
                )}
                {it.port}
              </div>
            )
          })}
        </div>
      ))}
    </div>
  )
}

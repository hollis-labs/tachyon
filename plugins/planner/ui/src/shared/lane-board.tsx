import { useState, type ReactNode } from 'react'
import {
  DndContext,
  DragOverlay,
  KeyboardSensor,
  PointerSensor,
  useDraggable,
  useDroppable,
  useSensor,
  useSensors,
  type Announcements,
  type DragEndEvent,
  type KeyboardCoordinateGetter,
} from '@dnd-kit/core'
import { cn } from '@hollis-labs/design-components'
import { CAPTION, Kbd, StateDot, type DotState } from './primitives'

export interface LaneCard {
  id: string
  content: ReactNode
  /** Locked while its own write is in flight: it cannot be re-dragged. */
  pending?: boolean
}

export interface Lane {
  id: string
  title: string
  cards: readonly LaneCard[]
  /** Collapsed lanes show only a toggle line, and still accept drops. */
  collapsed?: boolean
  /** Dot state of the lane heading. */
  state?: DotState
  /** Cards shown before "+N more". */
  limit?: number
  /** "Add task" row at the foot of the lane; omit for none. */
  add?: { label: string; key?: string; onAdd?: () => void; disabled?: boolean; reason?: string }
  /** Shown when the lane has no cards. */
  emptyText?: string
}

export interface LaneBoardLabels {
  /** "{lane}, {n} tasks": the list's accessible name. */
  lane: (title: string, count: number) => string
  /** "Drop a task here to {lane}" */
  empty: (title: string) => string
  pickedUp: (card: string, lane: string) => string
  movedOver: (card: string, lane: string) => string
  dropped: (card: string, lane: string) => string
  cancelled: (card: string) => string
  pending: string
  /** "+14 more" */
  more?: (n: number) => string
  /** Toggle line of a collapsed lane: "Done 15 · collapsed, click to expand". */
  collapsed?: (title: string, n: number) => string
  /** Placeholder where a dragged card came from. */
  wasHere?: string
  /** Hint for the drag handle. */
  dragHint: string
}

export interface LaneBoardProps {
  lanes: readonly Lane[]
  labels: LaneBoardLabels
  /** Card title lookup for announcements. */
  cardTitle: (id: string) => string
  /** Resolves when the write is accepted; rejection snaps the card back (host's job). */
  onMove: (cardId: string, fromLane: string, toLane: string) => void | Promise<void>
  /** Lanes that only show: cards cannot be dragged. */
  readOnly?: boolean
  className?: string
}

// Arrow keys move between lanes: Left/Right pick the neighbouring lane, so the
// keyboard flow is Space (pick up), arrows, Space (drop), Esc (cancel).
const laneCoordinates: KeyboardCoordinateGetter = (event, { context: { active, droppableRects, droppableContainers, collisionRect } }) => {
  if (!active || !collisionRect) return undefined
  const dir = event.code === 'ArrowRight' ? 1 : event.code === 'ArrowLeft' ? -1 : 0
  if (!dir) return undefined
  event.preventDefault()
  const lanes = droppableContainers
    .getEnabled()
    .flatMap((c) => {
      const r = droppableRects.get(c.id)
      return r ? [{ r }] : []
    })
    .sort((a, b) => a.r.left - b.r.left)
  const here = lanes.findIndex((l) => collisionRect.left + collisionRect.width / 2 >= l.r.left && collisionRect.left + collisionRect.width / 2 <= l.r.left + l.r.width)
  const next = lanes[Math.max(0, Math.min(lanes.length - 1, (here < 0 ? 0 : here) + dir))]
  return next ? { x: next.r.left + next.r.width / 2 - collisionRect.width / 2, y: next.r.top + 8 } : undefined
}

/** Lanes of cards with pointer and keyboard drag between lanes. Optimism, Undo and errors belong to the host. */
export function LaneBoard({ lanes, labels, cardTitle, onMove, readOnly, className }: LaneBoardProps) {
  const sensors = useSensors(useSensor(PointerSensor, { activationConstraint: { distance: 6 } }), useSensor(KeyboardSensor, { coordinateGetter: laneCoordinates }))
  const [active, setActive] = useState<string | null>(null)
  const [expanded, setExpanded] = useState<ReadonlySet<string>>(new Set())
  const laneOf = (cardId: string) => lanes.find((l) => l.cards.some((c) => c.id === cardId))
  const laneTitle = (id: unknown) => lanes.find((l) => l.id === id)?.title ?? ''

  const onDragEnd = (e: DragEndEvent) => {
    setActive(null)
    const from = laneOf(String(e.active.id))
    const to = e.over?.id
    if (from && to && from.id !== to) void onMove(String(e.active.id), from.id, String(to))
  }
  const announcements: Announcements = {
    onDragStart: ({ active: a }) => labels.pickedUp(cardTitle(String(a.id)), laneOf(String(a.id))?.title ?? ''),
    onDragOver: ({ active: a, over }) => (over ? labels.movedOver(cardTitle(String(a.id)), laneTitle(over.id)) : undefined),
    onDragEnd: ({ active: a, over }) => (over ? labels.dropped(cardTitle(String(a.id)), laneTitle(over.id)) : labels.cancelled(cardTitle(String(a.id)))),
    onDragCancel: ({ active: a }) => labels.cancelled(cardTitle(String(a.id))),
  }
  const activeCard = active ? laneOf(active)?.cards.find((c) => c.id === active) : undefined

  const open = lanes.filter((l) => !l.collapsed || expanded.has(l.id))
  const closed = lanes.filter((l) => l.collapsed && !expanded.has(l.id))
  const toggle = (id: string) => setExpanded((e) => { const n = new Set(e); if (!n.delete(id)) n.add(id); return n })
  return (
    <DndContext sensors={sensors} accessibility={{ announcements }} onDragStart={(e) => setActive(String(e.active.id))} onDragEnd={onDragEnd} onDragCancel={() => setActive(null)}>
      <div className={cn('flex min-w-0 flex-col gap-3', className)}>
        <div className="grid min-w-0 auto-cols-fr grid-flow-col gap-3 max-sm:flex max-sm:snap-x max-sm:overflow-x-auto">
          {open.map((l) => (
            <LaneColumn key={l.id} lane={l} labels={labels} active={active} readOnly={readOnly} onCollapse={l.collapsed ? () => toggle(l.id) : undefined} />
          ))}
        </div>
        {closed.map((l) => (
          <CollapsedLane key={l.id} lane={l} labels={labels} dragging={active !== null} onOpen={() => toggle(l.id)} />
        ))}
      </div>
      <DragOverlay>{activeCard && <div className="-rotate-1 rounded-control border border-primary bg-bg-elevated px-2 py-1.5 text-xs shadow-lg">{activeCard.content}</div>}</DragOverlay>
    </DndContext>
  )
}

function CollapsedLane({ lane, labels, dragging, onOpen }: { lane: Lane; labels: LaneBoardLabels; dragging: boolean; onOpen: () => void }) {
  const { setNodeRef, isOver } = useDroppable({ id: lane.id })
  return (
    <button ref={setNodeRef} type="button" onClick={onOpen} className={cn('rounded-control border border-transparent px-1 py-1.5 text-left', CAPTION, 'text-fg-faint hover:text-fg-secondary', dragging && 'border-dashed border-border', isOver && 'border-fg-muted bg-selection text-fg')}>
      {labels.collapsed ? labels.collapsed(lane.title, lane.cards.length) : `${lane.title} ${lane.cards.length}`}
    </button>
  )
}

function LaneColumn({ lane, labels, active, readOnly, onCollapse }: { lane: Lane; labels: LaneBoardLabels; active: string | null; readOnly?: boolean; onCollapse?: () => void }) {
  const { setNodeRef, isOver } = useDroppable({ id: lane.id })
  const [all, setAll] = useState(false)
  const limit = lane.limit ?? 3
  const shown = all || lane.cards.length <= limit + 1 ? lane.cards : lane.cards.slice(0, limit)
  const hidden = lane.cards.length - shown.length
  return (
    <section ref={setNodeRef} className={cn('flex min-h-56 min-w-0 snap-start flex-col max-sm:w-60 max-sm:flex-none rounded-panel border border-border', isOver && 'border-fg-muted bg-selection')}>
      <h3 className="flex items-center gap-1.5 border-b border-border-subtle px-2.5 py-2">
        <StateDot state={lane.state ?? 'todo'} />
        {onCollapse ? <button type="button" onClick={onCollapse} className={CAPTION}>{lane.title}</button> : <span className={CAPTION}>{lane.title}</span>}
        <span className="ml-auto font-mono text-caption tabular-nums text-fg-muted">{lane.cards.length}</span>
      </h3>
      <div className="flex flex-1 flex-col gap-1.5 p-2">
        <ul aria-label={labels.lane(lane.title, lane.cards.length)} className="flex flex-col gap-1.5">
          {lane.cards.length === 0 && <li className="px-1 py-3 font-mono text-caption text-fg-faint">{lane.emptyText ?? labels.empty(lane.title)}</li>}
          {shown.map((c) => (
            <CardItem key={c.id} card={c} labels={labels} readOnly={readOnly} blocked={lane.state === 'blocked'} lifted={active === c.id} />
          ))}
        </ul>
        {hidden > 0 && (
          <button type="button" onClick={() => setAll(true)} className="self-start px-1 font-mono text-caption text-fg-faint hover:text-fg-secondary">{labels.more ? labels.more(hidden) : `+${hidden}`}</button>
        )}
        {lane.add && (
          <button
            type="button"
            disabled={lane.add.disabled}
            title={lane.add.reason}
            onClick={lane.add.onAdd}
            className="mt-auto flex h-6 items-center gap-1.5 rounded-sm px-1.5 text-label text-fg-faint enabled:hover:bg-selection enabled:hover:text-fg-secondary disabled:cursor-not-allowed disabled:opacity-60"
          >
            {lane.add.label}
            {lane.add.key && <Kbd className="ml-auto">{lane.add.key}</Kbd>}
          </button>
        )}
      </div>
    </section>
  )
}

function CardItem({ card, labels, readOnly, blocked, lifted }: { card: LaneCard; labels: LaneBoardLabels; readOnly?: boolean; blocked?: boolean; lifted?: boolean }) {
  const { setNodeRef, attributes, listeners } = useDraggable({ id: card.id, disabled: card.pending || readOnly })
  if (lifted) {
    return (
      <li ref={setNodeRef} {...attributes} {...listeners} className="grid h-11 place-items-center rounded-control border border-dashed border-fg-muted font-mono text-caption uppercase tracking-label text-fg-muted">{labels.wasHere ?? ''}</li>
    )
  }
  return (
    <li ref={setNodeRef} {...(readOnly ? {} : { ...attributes, ...listeners })} aria-label={undefined} aria-roledescription={readOnly ? undefined : labels.dragHint} aria-busy={card.pending || undefined} className={cn('flex min-w-0 flex-col gap-0.5 break-words rounded-control border border-border-subtle bg-bg-elevated px-2 py-1.5 text-xs leading-snug outline-none hover:border-surface-hover focus-visible:outline focus-visible:outline-1 focus-visible:outline-ring', blocked && 'border-l-2 border-l-danger', card.pending && 'opacity-70')}>
      {card.content}
      {card.pending && <span className="text-caption text-fg-faint">{labels.pending}</span>}
    </li>
  )
}

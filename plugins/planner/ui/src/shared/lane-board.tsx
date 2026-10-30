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
  /** Collapsed lanes show only the heading and count, and still accept drops. */
  collapsed?: boolean
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
export function LaneBoard({ lanes, labels, cardTitle, onMove, className }: LaneBoardProps) {
  const sensors = useSensors(useSensor(PointerSensor, { activationConstraint: { distance: 6 } }), useSensor(KeyboardSensor, { coordinateGetter: laneCoordinates }))
  const [active, setActive] = useState<string | null>(null)
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

  return (
    <DndContext sensors={sensors} accessibility={{ announcements }} onDragStart={(e) => setActive(String(e.active.id))} onDragEnd={onDragEnd} onDragCancel={() => setActive(null)}>
      <div className={cn('flex min-w-0 snap-x gap-3 overflow-x-auto', className)}>
        {lanes.map((l) => (
          <LaneColumn key={l.id} lane={l} labels={labels} dragging={active !== null} />
        ))}
      </div>
      <DragOverlay>{activeCard && <div className="rounded-md border border-border bg-panel p-2 shadow-lg">{activeCard.content}</div>}</DragOverlay>
    </DndContext>
  )
}

function LaneColumn({ lane, labels, dragging }: { lane: Lane; labels: LaneBoardLabels; dragging: boolean }) {
  const { setNodeRef, isOver } = useDroppable({ id: lane.id })
  const collapsed = lane.collapsed && !dragging
  return (
    <section ref={setNodeRef} className={cn('flex shrink-0 snap-start flex-col gap-2 rounded-md border border-border-subtle p-2', collapsed ? 'w-40' : 'w-64 flex-1', isOver && 'bg-panel-hover')}>
      <h3 className="flex items-center gap-2 font-mono dash-caption uppercase tracking-label text-fg-muted">
        {lane.title}
        <span className="tabular-nums text-fg-faint">{lane.cards.length}</span>
      </h3>
      {!collapsed && (
        <ul aria-label={labels.lane(lane.title, lane.cards.length)} className="flex min-h-12 flex-col gap-2">
          {lane.cards.length === 0 && <li className="rounded-md border border-dashed border-border-subtle p-3 dash-caption text-fg-faint">{labels.empty(lane.title)}</li>}
          {lane.cards.map((c) => (
            <CardItem key={c.id} card={c} labels={labels} />
          ))}
        </ul>
      )}
    </section>
  )
}

function CardItem({ card, labels }: { card: LaneCard; labels: LaneBoardLabels }) {
  const { setNodeRef, attributes, listeners, isDragging } = useDraggable({ id: card.id, disabled: card.pending })
  return (
    <li ref={setNodeRef} {...attributes} {...listeners} aria-label={undefined} aria-roledescription={labels.dragHint} aria-busy={card.pending || undefined} className={cn('rounded-md border border-border bg-panel p-2 outline-none focus-visible:outline-2', isDragging && 'opacity-40', card.pending && 'opacity-70')}>
      {card.content}
      {card.pending && <span className="mt-1 block dash-caption text-fg-faint">{labels.pending}</span>}
    </li>
  )
}

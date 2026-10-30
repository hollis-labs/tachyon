import type { ReactNode } from 'react'
import { useState } from 'react'
import { DndContext, DragOverlay, PointerSensor, useDraggable, useDroppable, useSensor, useSensors } from '@dnd-kit/core'
import { cn } from '@hollis-labs/design-components'

export interface LinkAnchorLabels {
  /** Accessible name of the port: "Link {title}". */
  port: (title: string) => string
  /** Text following the pointer while dragging. */
  dragging: (title: string) => string
}

/** Context for ports and targets inside it. Dropping a port on another item's zone calls onLink(from, to). */
export function LinkLayer({ onLink, titleOf, labels, children }: { onLink: (from: string, to: string) => void; titleOf: (id: string) => string; labels: LinkAnchorLabels; children: ReactNode }) {
  const sensors = useSensors(useSensor(PointerSensor, { activationConstraint: { distance: 6 } }))
  const [from, setFrom] = useState<string | null>(null)
  return (
    <DndContext
      sensors={sensors}
      onDragStart={(e) => setFrom(String(e.active.id).replace(/^port:/, ''))}
      onDragCancel={() => setFrom(null)}
      onDragEnd={(e) => {
        setFrom(null)
        const from = String(e.active.id).replace(/^port:/, '')
        const to = e.over ? String(e.over.id).replace(/^zone:/, '') : ''
        if (to && to !== from) onLink(from, to)
      }}
    >
      {children}
      <DragOverlay dropAnimation={null}>
        {from && <span className="rounded-md border border-dashed border-border bg-panel px-2 py-0.5 dash-caption text-fg">{labels.dragging(titleOf(from))}</span>}
      </DragOverlay>
    </DndContext>
  )
}

/**
 * Sits inside a relatively positioned row: an invisible drop zone over the
 * whole row and a small port on its right edge, shown on hover or focus. The
 * keyboard route to the same action is the host's (L opens a "Link to..." palette).
 */
export function LinkPort({ id, title, labels, className }: { id: string; title: string; labels: LinkAnchorLabels; className?: string }) {
  const drag = useDraggable({ id: `port:${id}` })
  const drop = useDroppable({ id: `zone:${id}` })
  return (
    <>
      <span ref={drop.setNodeRef} aria-hidden className={cn('pointer-events-none absolute inset-0', drop.isOver && 'border border-dashed border-primary')} />
      <button
        ref={drag.setNodeRef}
        type="button"
        aria-label={labels.port(title)}
        {...drag.listeners}
        {...drag.attributes}
        tabIndex={-1}
        onClick={(e) => e.stopPropagation()}
        className={cn('absolute right-1 top-1/2 size-2.5 -translate-y-1/2 cursor-grab rounded-full border border-border bg-panel opacity-0 group-hover:opacity-100 group-focus-visible:opacity-100 focus-visible:opacity-100', drag.isDragging && 'opacity-100', className)}
      />
    </>
  )
}

/** Relation tag with the dashed bracket mark, shown on an item that is linked. */
export function LinkTag({ children }: { children: ReactNode }) {
  return <span className="border-l border-dashed border-border pl-1.5 dash-caption text-fg-muted">{children}</span>
}

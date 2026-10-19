import { CSS } from '@dnd-kit/utilities'
import { useSortable } from '@dnd-kit/sortable'
import { GripVertical, MessageSquare } from 'lucide-react'
import { PlannerStatus } from '@planner/components/planner/planner-status'
import { PlannerFact } from '@planner/components/planner/planner-fact'
import { PlannerRowContent } from '@planner/components/planner/planner-table'
import { itemFacts } from '@planner/lib/planner'
import type { PlannerDb, PlannerItem } from '@planner/api/types'

interface PlannerRowProps {
  db: PlannerDb
  item: PlannerItem
  /** Drag to reorder; off while the list is filtered. */
  sortable: boolean
  onOpen: () => void
}

export function PlannerRow({ db, item, sortable, onOpen }: PlannerRowProps) {
  const { attributes, listeners, setNodeRef, transform, transition, isDragging } = useSortable({ id: item.id, disabled: !sortable })
  const facts = itemFacts(db, item)
  const comments = item.comments?.length ?? 0

  return (
    <li
      ref={setNodeRef}
      style={{ transform: CSS.Transform.toString(transform), transition }}
      className={`dash-record-list-row ${isDragging ? 'bg-panel-hover opacity-70' : ''}`}
    >
      <button
        type="button"
        className={`planner-row-grip cursor-grab touch-none rounded text-fg-faint focus-visible:outline focus-visible:outline-ring ${sortable ? '' : 'invisible'}`}
        aria-label={`Drag to reorder ${item.id}`}
        {...attributes}
        {...listeners}
        tabIndex={sortable ? 0 : -1}
      >
        <GripVertical className="h-3 w-3" />
      </button>
      <button type="button" className="planner-row-trigger dash-record-grid" onClick={onOpen} aria-label={`Open ${item.id}: ${item.title}`}>
        <PlannerRowContent title={item.title} metadata={<>
            <span className="shrink-0">{item.id}</span>
            {facts.map((f) => <PlannerFact key={f} db={db} fact={f} />)}
            {item.tldr && <span className="min-w-0 overflow-hidden text-ellipsis" title={item.tldr}>{item.tldr}</span>}
          </>} status={<PlannerStatus status={item.status} />} trailing={<span className="inline-flex items-center gap-1" title={`${comments} comments`}>
          {comments > 0 && <><MessageSquare className="size-3" />{comments}</>}
        </span>} />
      </button>
    </li>
  )
}

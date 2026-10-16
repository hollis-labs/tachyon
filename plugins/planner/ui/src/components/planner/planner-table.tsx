import type { ReactNode } from 'react'

/** Operations' two-line row rhythm, shared by every Planner list and board. */
export function PlannerRowContent({ title, metadata, status, trailing }: { title: string; metadata: ReactNode; status: ReactNode; trailing?: ReactNode }) {
  return <>
    <span className="planner-cell-main">
      <span className="planner-cell-title" title={title}>{title}</span>
      <span className="planner-cell-meta">{metadata}</span>
    </span>
    <span className="planner-cell-status">{status}</span>
    <span className="planner-cell-trailing">{trailing}</span>
  </>
}

export function PlannerTableHeader({ trailing = 'Comments' }: { trailing?: string }) {
  return <div className="planner-table-header planner-table-grid" aria-hidden="true">
    <span>Item</span>
    <span>Status</span>
    <span className="text-right" title={trailing}>{trailing === 'Comments' ? 'Replies' : trailing}</span>
  </div>
}

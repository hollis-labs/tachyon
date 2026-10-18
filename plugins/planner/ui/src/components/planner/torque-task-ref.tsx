import { Link } from 'react-router-dom'
import { PlannerStatus } from '@planner/components/planner/planner-status'
import type { PlannerTaskBrief } from '@planner/api/types'

/**
 * A reference to a Torque task: its id, and once known its live title and
 * status. `brief` is undefined while loading and null when Torque has no such task.
 */
export function TorqueTaskRef({ id, brief, label }: { id: string; brief: PlannerTaskBrief | null | undefined; label?: string }) {
  return (
    <span className="inline-flex min-w-0 max-w-full flex-wrap items-center gap-x-2 gap-y-1">
      <Link to={`/tasks/${encodeURIComponent(id)}`} className="font-mono dash-label leading-4 text-info hover:underline">
        {id}
      </Link>
      {label && <span className="min-w-0 break-words dash-body leading-5 text-fg-muted">{label}</span>}
      {brief && <span className="min-w-0 max-w-full break-words dash-body leading-5">{brief.title}</span>}
      {brief && <PlannerStatus status={brief.status} />}
      {brief === null && <span className="dash-label leading-4 text-fg-muted">not found in Torque</span>}
    </span>
  )
}

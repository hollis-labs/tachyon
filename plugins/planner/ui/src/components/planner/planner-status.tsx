import { StatusBadge, type StatusKey } from '@hollis-labs/kit-dashboard'
import { statusToneKey } from '@planner/lib/planner'

/** App vocabulary maps to the kit's shared status presentation. */
export function PlannerStatus({ status, className = '', toneKey }: { status?: string; className?: string; toneKey?: StatusKey }) {
  return status ? <StatusBadge status={status} tone={toneKey ?? statusToneKey(status)} className={`shrink-0 whitespace-nowrap ${className}`} /> : null
}

import { statusTone } from '@hollis-labs/kit-dashboard'
import { statusToneKey } from '@planner/lib/planner'

/** A planner status pill. The planner has its own vocabulary; it borrows the kit's colors. */
export function PlannerStatus({ status, className = '', toneKey }: { status?: string; className?: string; toneKey?: Parameters<typeof statusTone>[0] }) {
  if (!status) return null
  const tone = statusTone(toneKey ?? statusToneKey(status))
  return (
    <span
      className={`planner-status inline-flex shrink-0 items-center gap-2 whitespace-nowrap rounded border px-2 py-1 font-mono text-label leading-4 uppercase tracking-[0.14em] ${tone.border} ${tone.bg} ${tone.text} ${className}`}
    >
      <span className={`h-1.5 w-1.5 rounded-full ${tone.dot}`} />
      <span>{status}</span>
    </span>
  )
}

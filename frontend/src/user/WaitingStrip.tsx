import { Link } from "react-router-dom"
import type { InboxSummary } from "./approvalsApi"
import type { Remote } from "./useRemote"

const CHIP =
  "inline-flex h-[22px] items-center gap-1.5 rounded-full border border-border px-2 text-label text-fg-secondary hover:bg-selection"

/** Approvals count now; Alerts waits for GET /alerts/counts. Whole strip hidden at zero or unavailable. */
export function WaitingStrip({ inbox }: { inbox: Remote<InboxSummary> }) {
  if (inbox.status !== "ready" || inbox.data.counts.pending === 0) return null
  return (
    <nav aria-label="Waiting on you" className="flex flex-wrap items-center gap-2 px-0.5 text-xs">
      <b className="flex-1 text-fg">Waiting on you</b>
      <Link to="/approvals" className={CHIP}>
        Approvals <b className="text-fg">{inbox.data.counts.pending}</b>
      </Link>
    </nav>
  )
}

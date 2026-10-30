import { Link } from "react-router-dom"
import type { InboxSummary } from "./approvalsApi"
import type { Remote } from "./useRemote"

/** Approvals count now; Alerts waits for a counts endpoint. Hidden at zero or unavailable. */
export function WaitingStrip({ inbox }: { inbox: Remote<InboxSummary> }) {
  if (inbox.status !== "ready" || inbox.data.counts.pending === 0) return null
  const n = inbox.data.counts.pending
  return (
    <nav aria-label="Waiting on you" className="flex flex-wrap items-center gap-2 text-sm">
      <span className="text-text-subtle">Waiting on you</span>
      <Link
        to="/approvals"
        className="rounded-full border border-border px-3 py-0.5 text-foreground hover:bg-panel-hover"
      >
        Approvals {n}
      </Link>
    </nav>
  )
}

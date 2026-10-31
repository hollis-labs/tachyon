import { Link } from "react-router-dom"
import type { AlertCounts, InboxSummary } from "./approvalsApi"
import type { Remote } from "./useRemote"

const CHIP =
  "inline-flex h-[22px] items-center gap-1.5 rounded-full border border-border px-2 text-label text-fg-secondary hover:bg-selection"

/** Approvals and Alerts counts. Each chip hides at zero or unavailable; the whole strip hides when both do. */
export function WaitingStrip({
  inbox,
  alerts,
}: {
  inbox: Remote<InboxSummary>
  alerts: Remote<AlertCounts>
}) {
  const approvals = inbox.status === "ready" ? inbox.data.counts.pending : 0
  const needs = alerts.status === "ready" ? alerts.data.needs_you : 0
  if (approvals === 0 && needs === 0) return null
  return (
    <nav aria-label="Waiting on you" className="flex flex-wrap items-center gap-2 px-0.5 text-xs">
      <b className="flex-1 text-fg">Waiting on you</b>
      {approvals > 0 && (
        <Link to="/approvals" className={CHIP}>
          Approvals <b className="text-fg">{approvals}</b>
        </Link>
      )}
      {needs > 0 && (
        <button
          type="button"
          className={CHIP}
          onClick={() => document.querySelector<HTMLElement>("[data-alert-trigger]")?.click()}
        >
          Alerts <b className="text-fg">{needs}</b>
        </button>
      )}
    </nav>
  )
}

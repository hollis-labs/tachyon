import { Link } from "react-router-dom"
import type { AlertCounts, InboxSummary, NeedsYouCount } from "./approvalsApi"
import type { Remote } from "./useRemote"

const CHIP =
  "inline-flex h-[22px] items-center gap-1.5 rounded-full border border-border px-2 text-label text-fg-secondary hover:bg-selection"

/** Needs-you, Approvals and Alerts counts. Each chip hides at zero or unavailable; the whole strip hides when all do. */
export function WaitingStrip({
  inbox,
  alerts,
  needsYou,
}: {
  inbox: Remote<InboxSummary>
  alerts: Remote<AlertCounts>
  needsYou?: Remote<NeedsYouCount>
}) {
  const approvals = inbox.status === "ready" ? inbox.data.counts.pending : 0
  const needs = alerts.status === "ready" ? alerts.data.needs_you : 0
  const waiting = needsYou?.status === "ready" ? needsYou.data.items.length : 0
  const critical = needsYou?.status === "ready" && needsYou.data.items.some((i) => i.critical)
  if (approvals === 0 && needs === 0 && waiting === 0) return null
  return (
    <nav aria-label="Waiting on you" className="flex flex-wrap items-center gap-2 px-0.5 text-xs">
      <b className="flex-1 text-fg">Waiting on you</b>
      {waiting > 0 && (
        <Link
          to="/needs-you"
          className={`${CHIP} ${critical ? "border-danger/50 text-danger" : ""}`}
        >
          Needs you <b className={critical ? "text-danger" : "text-fg"}>{waiting}</b>
        </Link>
      )}
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

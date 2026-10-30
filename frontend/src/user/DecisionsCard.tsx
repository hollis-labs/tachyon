import { Link } from "react-router-dom"
import { Card } from "../settings/Card"
import type { DecisionEvent, History } from "./approvalsApi"
import type { Remote } from "./useRemote"

const DECIDED = new Set(["approved", "denied"])

export function outcomeLabel(state: string): "Approved" | "Denied" | null {
  if (state === "approved") return "Approved"
  if (state === "denied") return "Denied"
  return null
}

export function relativeTime(iso: string, now = Date.now()): string {
  const s = Math.max(0, Math.round((now - Date.parse(iso)) / 1000))
  if (Number.isNaN(s)) return ""
  if (s < 60) return "just now"
  if (s < 3600) return `${Math.floor(s / 60)}m ago`
  if (s < 86400) return `${Math.floor(s / 3600)}h ago`
  if (s < 172800) return "Yesterday"
  return `${Math.floor(s / 86400)}d ago`
}

/** Hidden until the approvals history can be read; a failure here never shows an error. */
export function DecisionsCard({ history }: { history: Remote<History> }) {
  if (history.status !== "ready") return null
  const rows: DecisionEvent[] = history.data.events.filter((e) => DECIDED.has(e.status)).slice(0, 5)
  return (
    <Card title="Your recent decisions">
      {rows.length === 0 ? (
        <p className="text-sm text-text-subtle">Nothing decided yet</p>
      ) : (
        <ul className="divide-y divide-border-subtle">
          {rows.map((i) => (
            <li
              key={i.event_id}
              className="flex flex-col gap-1 py-2 sm:flex-row sm:items-center sm:gap-3"
            >
              <span
                className={`w-fit rounded-full border px-2 py-0.5 text-xs ${
                  i.status === "denied"
                    ? "border-danger text-danger"
                    : "border-border text-foreground"
                }`}
              >
                {outcomeLabel(i.status)}
              </span>
              <div className="min-w-0 flex-1">
                <p className="truncate text-sm text-foreground">{i.mini.title}</p>
                <p className="truncate text-xs text-text-subtle">{i.kind}</p>
              </div>
              <span className="text-xs text-text-subtle">{relativeTime(i.at)}</span>
            </li>
          ))}
        </ul>
      )}
      {rows.length > 0 && (
        <Link to="/approvals" className="mt-2 inline-block text-sm text-text-subtle underline">
          See all in Approvals
        </Link>
      )}
    </Card>
  )
}

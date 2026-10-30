import { Link } from "react-router-dom"
import { Chip, SetCard, SetRow } from "../settings/parts"
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
    <SetCard>
      <SetRow title="Your recent decisions" help="Approvals you answered. Newest first.">
        {rows.length > 0 && (
          <Link to="/approvals" className="text-xs text-fg underline">
            See all in Approvals
          </Link>
        )}
      </SetRow>
      {rows.length === 0 ? (
        <div className="flex flex-col items-center gap-0.5 border-t border-border-subtle px-4 py-[26px] text-center">
          <b className="text-fg">Nothing decided yet</b>
          <span className="text-xs text-fg-muted">Approvals you answer will show up here.</span>
        </div>
      ) : (
        <ul>
          {rows.map((i) => (
            <li
              key={i.event_id}
              className="flex items-center gap-3 border-t border-border-subtle px-4 py-2.5"
            >
              <span className="flex w-[72px] shrink-0 justify-center [&>span]:w-full [&>span]:justify-center">
                <Chip tone={i.status === "denied" ? "danger" : undefined}>
                  {outcomeLabel(i.status)}
                </Chip>
              </span>
              <div className="flex min-w-0 flex-1 flex-col">
                <b className="truncate text-fg">{i.mini.title}</b>
                <span className="truncate text-xs text-fg-muted">{i.mini.group ?? i.kind}</span>
              </div>
              <span className="whitespace-nowrap font-mono text-label text-fg-muted">
                {relativeTime(i.at)}
              </span>
            </li>
          ))}
        </ul>
      )}
    </SetCard>
  )
}

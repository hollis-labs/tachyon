import { useEffect, useState } from "react"
import type { ApprovalClient, AuditEvent } from "./client"
import { ago } from "./parts"

const label = "font-mono dash-caption uppercase tracking-wide text-fg-faint"
const words = (k: string) => k.replace(/_/g, " ").replace(/^./, (c) => c.toUpperCase())

// Everything that happened to one request: presented, deferred, resolved, who and when. Loaded only
// once opened.
export function AuditTrail({ id, client }: { id: string; client: ApprovalClient }) {
  const [open, setOpen] = useState(false)
  const [events, setEvents] = useState<AuditEvent[] | null>(null)
  const [error, setError] = useState("")
  useEffect(() => {
    if (!open || events) return
    client.audit(id).then((r) => setEvents(r.events)).catch((e) => setError(e instanceof Error ? e.message : "Could not load the audit trail"))
  }, [open, events, client, id])
  return (
    <details onToggle={(e) => setOpen(e.currentTarget.open)}>
      <summary className="cursor-pointer dash-control text-fg-muted">Audit trail</summary>
      {error && <p role="alert" className="mt-2 dash-support text-danger">{error}</p>}
      {open && !events && !error && <p role="status" className="mt-2 dash-support text-fg-faint">Loading…</p>}
      {events && !events.length && <p className="mt-2 dash-support text-fg-faint">No recorded events.</p>}
      {!!events?.length && (
        <ol aria-label="Audit trail" className="mt-2 space-y-2">
          {events.map((e) => (
            <li key={e.event_id}>
              <span className="dash-body text-fg">{words(e.decision ?? e.kind)}</span>
              <span className={`block ${label} normal-case`} title={new Date(e.ts).toLocaleString()}>{e.actor.label} · {ago(e.ts)}</span>
              {(e.note || e.reason) && <span className="block dash-support text-fg-muted">{e.note ?? e.reason}</span>}
            </li>
          ))}
        </ol>
      )}
    </details>
  )
}

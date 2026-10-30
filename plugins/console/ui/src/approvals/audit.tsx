import { useEffect, useState } from "react"
import { Button } from "@hollis-labs/design-components"
import type { ApprovalClient, AuditEvent } from "./client"
import { ago } from "./parts"

const words = (k: string) => k.replace(/_/g, " ").replace(/^./, (c) => c.toUpperCase())

// Everything that happened to one request: presented, deferred, resolved, who and when.
export function AuditTrail({ id, client }: { id: string; client: ApprovalClient }) {
  const [events, setEvents] = useState<AuditEvent[] | null>(null)
  const [error, setError] = useState("")
  const [attempt, setAttempt] = useState(0)
  useEffect(() => {
    setError("")
    let live = true
    client.audit(id).then((r) => live && setEvents(r.events)).catch((e) => live && setError(e instanceof Error ? e.message : "Could not load the audit trail"))
    return () => { live = false }
  }, [client, id, attempt])
  return (
    <section aria-label="Decision audit">
      <h3 className="font-mono dash-caption uppercase tracking-label text-fg-faint">Decision audit{events ? ` (${events.length})` : ""}</h3>
      {error && (
        <p role="alert" className="mt-2 flex items-center gap-2 dash-support text-danger">
          Could not load the audit trail.
          <Button variant="outline" size="sm" onClick={() => setAttempt((n) => n + 1)}>Retry</Button>
        </p>
      )}
      {!events && !error && <p role="status" className="mt-2 dash-support text-fg-faint">Loading…</p>}
      {events && !events.length && <p className="mt-2 dash-support text-fg-faint">No recorded events.</p>}
      {!!events?.length && (
        <ol aria-label="Audit trail" className="mt-2 space-y-2">
          {events.map((e) => (
            <li key={e.event_id}>
              <span className="dash-control text-fg" title={new Date(e.ts).toLocaleString()}>{words(e.decision ?? e.kind)} by {e.actor.label === "You" ? "you" : e.actor.label}, {ago(e.ts)}</span>
              {(e.note || e.reason) && <span className="block dash-support text-fg-muted">{e.note ?? e.reason}</span>}
            </li>
          ))}
        </ol>
      )}
    </section>
  )
}

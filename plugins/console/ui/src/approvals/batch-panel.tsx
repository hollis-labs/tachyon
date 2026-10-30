import { Button } from "@hollis-labs/design-components"
import { Chip } from "../components/chip"
import { InitialsAvatar } from "../components/initials-avatar"
import { Keycap } from "../components/keycap"
import type { ApprovalItem } from "./client"
import { MAX_BATCH } from "./batch"
import { RiskChip } from "./parts"

const label = "font-mono dash-caption uppercase tracking-label text-fg-faint"
const who = (i: ApprovalItem) => i.request_snapshot.source.agent_label ?? i.request_snapshot.source.agent_id

// Right pane while requests are ticked: what a batch decision covers, and the decision itself.
export function BatchPanel({ items, confirming, busy, accessNeeded, onConfirm, onCancel, onBack, onRun, onOpen }: {
  items: ApprovalItem[]
  confirming?: "approved" | "denied"
  busy: boolean
  // Set when the operator may not decide in bulk; the decision buttons stay off.
  accessNeeded?: boolean
  onConfirm(d: "approved" | "denied"): void
  onCancel(): void
  onBack(): void
  onRun(d: "approved" | "denied"): void
  onOpen(id: string): void
}) {
  const n = items.length
  const noun = n === 1 ? "request" : "requests"
  const risks = [...new Set(items.map((i) => i.request_snapshot.class ?? "medium"))]
  return (
    <div className="flex h-full min-h-0 flex-col">
      <div className="min-h-0 flex-1 space-y-5 overflow-y-auto p-4">
        <header>
          <h2 className="font-sans normal-case tracking-normal dash-body font-semibold text-fg">Approve {n} {noun}</h2>
          <p className="mt-1 dash-support text-fg-muted">Low-risk requests the server marked safe to decide together. Each is still recorded on its own.</p>
        </header>
        {accessNeeded && (
          <p role="status" className="rounded border border-border p-3 dash-control text-fg-muted">
            Operator access needed. Batch decisions need elevated access; open each request to decide it on its own.
          </p>
        )}
        {n >= MAX_BATCH && <p className="dash-support text-fg-faint">Up to {MAX_BATCH} requests per batch.</p>}
        <section aria-label="Selected requests">
          <h3 className={label}>Selected</h3>
          <ul className="mt-2 divide-y divide-border rounded border border-border">
            {items.map((i) => (
              <li key={i.item_id} className="flex items-center gap-3 px-3 py-2">
                <InitialsAvatar name={who(i)} className="size-6" />
                <span className="min-w-0 flex-1 truncate dash-control text-fg">{i.request_snapshot.title}</span>
                <RiskChip risk={i.request_snapshot.class} />
                <Button variant="ghost" size="sm" className="dash-label" onClick={() => onOpen(i.item_id)} aria-label={`Details for ${i.request_snapshot.title}`}>Details</Button>
              </li>
            ))}
          </ul>
        </section>
        <section>
          <h3 className={label}>If approved</h3>
          <ul className="mt-2 list-disc space-y-1 pl-5 dash-control text-fg-muted">
            {[...new Set(items.map((i) => i.request_snapshot.impact?.approve ?? `${i.request_snapshot.title} proceeds.`))].map((t) => <li key={t}>{t}</li>)}
          </ul>
        </section>
        <section>
          <h3 className={label}>Rules</h3>
          <div className="mt-2 flex flex-wrap gap-2"><Chip tone="faint">{risks.join(", ")} risk only</Chip><Chip tone="faint">Same decision for each</Chip></div>
        </section>
      </div>
      <div role="toolbar" aria-label="Batch decision" className="space-y-3 border-t border-border bg-bg p-4">
        {confirming ? (
          <div className="flex flex-wrap items-center gap-3">
            <span className="dash-control text-fg-muted">{confirming === "approved" ? "Approve" : "Deny"} {n} {noun}? This cannot be undone.</span>
            <div className="ml-auto flex gap-2">
              <Button variant="outline" disabled={busy} onClick={onCancel}>Back</Button>
              <Button disabled={busy || accessNeeded} onClick={() => onRun(confirming)}>{busy ? "Working…" : `${confirming === "approved" ? "Approve" : "Deny"} all ${n}`} <Keycap>⌘↵</Keycap></Button>
            </div>
          </div>
        ) : (
          <div className="flex flex-wrap justify-end gap-2">
            <Button variant="ghost" className="mr-auto" onClick={onBack}>Back to queue</Button>
            <Button variant="outline" disabled={accessNeeded} onClick={() => onConfirm("denied")}>Deny {n}</Button>
            <Button disabled={accessNeeded} onClick={() => onConfirm("approved")}>Approve {n}</Button>
          </div>
        )}
      </div>
    </div>
  )
}

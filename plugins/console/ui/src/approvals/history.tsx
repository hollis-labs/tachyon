import { useCallback, useEffect, useState } from "react"
import { Button } from "@hollis-labs/design-components"
import { Check, CircleDot, Clock, X } from "lucide-react"
import type { ApprovalClient, HistoryEvent, HistoryFacets } from "./client"
import { Chip } from "../components/chip"
import { AuditTrail } from "./audit"
import { ago } from "./parts"

const VERB: Record<string, string> = {
  approved: "Approved",
  denied: "Denied",
  answered: "Answered",
  expired: "Expired",
  withdrawn: "Withdrawn",
}
function Marker({ e }: { e: HistoryEvent }) {
  const k = e.status ?? e.decision ?? ""
  const cls = k === "denied" ? "text-danger border-danger/60" : "text-fg-muted border-border"
  const Icon = k === "approved" ? Check : k === "denied" ? X : k === "expired" ? Clock : CircleDot
  return (
    <span aria-hidden className={`absolute -left-[calc(1rem+9px)] top-0.5 flex h-[18px] w-[18px] items-center justify-center rounded-full border bg-bg ${cls}`}>
      <Icon className="h-2.5 w-2.5" />
    </span>
  )
}
const REASON: Record<string, string> = { deadline: "Deadline passed" }
const reasonText = (e: HistoryEvent) => {
  const t = e.note ?? e.reason
  return t ? (REASON[t.trim().toLowerCase()] ?? t) : undefined
}
const verb = (e: HistoryEvent) => VERB[e.status ?? ""] ?? VERB[e.decision ?? ""] ?? e.kind.replace(/_/g, " ")

function dayLabel(at: string, now = new Date()) {
  const d = new Date(at)
  const start = (x: Date) => new Date(x.getFullYear(), x.getMonth(), x.getDate()).getTime()
  const diff = Math.round((start(now) - start(d)) / 86400000)
  if (diff === 0) return "Today"
  if (diff === 1) return "Yesterday"
  return d.toLocaleDateString([], { month: "short", day: "numeric", year: d.getFullYear() === now.getFullYear() ? undefined : "numeric" })
}

// History: what was decided, by whom and why, newest first, grouped under day pills.
export function HistoryTimeline({ client, search, status, requester, onOpen, onFacets }: { client: ApprovalClient; search: string; status: string; requester: string; onOpen(itemId: string): void; onFacets?(f: HistoryFacets | undefined): void }) {
  const [events, setEvents] = useState<HistoryEvent[] | null>(null)
  const [next, setNext] = useState<string>()
  const [error, setError] = useState("")
  const [loading, setLoading] = useState(false)
  const [audit, setAudit] = useState<string>()
  const filtered = !!(status || requester.trim() || search.trim())
  const load = useCallback(async (cursor?: string) => {
    setLoading(true)
    setError("")
    try {
      const page = await client.history({ q: search.trim(), status, requester: requester.trim(), cursor })
      setEvents((cur) => (cursor && cur ? [...cur, ...page.events] : page.events))
      setNext(page.next_cursor)
      if (!cursor) onFacets?.(page.facets)
    } catch (e) {
      setError(e instanceof Error ? e.message : "Could not load history")
    } finally {
      setLoading(false)
    }
  }, [client, search, status, requester, onFacets])
  useEffect(() => { void load() }, [load])
  // Newest first by event time, then one group per day, however the pages arrived.
  const days: { label: string; items: HistoryEvent[] }[] = []
  for (const e of [...(events ?? [])].sort((a, b) => Date.parse(b.at) - Date.parse(a.at))) {
    const label = dayLabel(e.at)
    const day = days.find((d) => d.label === label)
    if (day) day.items.push(e)
    else days.push({ label, items: [e] })
  }
  return (
    <div className="min-h-0 flex-1 overflow-y-auto p-4">
      {error && (
        <div role="alert" className="mb-3 flex items-center justify-between gap-2 rounded border border-danger/30 p-3 dash-control text-danger">
          <span>Could not load history: {error}</span>
          <Button variant="outline" size="sm" onClick={() => void load()}>Retry</Button>
        </div>
      )}
      {events && !events.length && !error && (
        <p className="py-10 text-center dash-body text-fg-faint">{filtered ? "Nothing matches these filters." : "No decisions yet."}</p>
      )}
      {!events && !error && <p className="py-10 text-center dash-body text-fg-faint">Loading history…</p>}
      <ol aria-label="Decision history" className="space-y-5">
        {days.map((d) => (
          <li key={d.label}>
            <h3 className="mb-2 normal-case tracking-normal"><span className="rounded-full border border-border px-2 py-0.5 font-sans dash-caption normal-case tracking-normal text-fg-muted">{d.label}</span></h3>
            <ul className="space-y-3 border-l border-border pl-4">
              {d.items.map((e) => (
                <li key={e.event_id} className="relative">
                  <Marker e={e} />
                  <button type="button" onClick={() => onOpen(e.item_id)} className="block w-full text-left">
                    <span className="flex flex-wrap items-center gap-2 dash-body text-fg"><span className="font-medium">{verb(e)}</span><Chip>{e.mini?.title ?? e.item_id}</Chip></span>
                    <span className="block font-mono dash-caption text-fg-faint" title={new Date(e.at).toLocaleString()}>{e.actor.label} · {ago(e.at)}</span>
                  </button>
                  {reasonText(e) && (
                    <p className="mt-2 rounded border border-border p-2 dash-support text-fg-muted">{reasonText(e)}</p>
                  )}
                  <button type="button" aria-expanded={audit === e.event_id} onClick={() => setAudit(audit === e.event_id ? undefined : e.event_id)} className="mt-1 dash-label text-fg-muted underline underline-offset-2 hover:text-fg">
                    {audit === e.event_id ? "Hide audit" : "Audit"}
                  </button>
                  {audit === e.event_id && <div className="mt-2"><AuditTrail id={e.item_id} client={client} /></div>}
                </li>
              ))}
            </ul>
          </li>
        ))}
      </ol>
      {next && <Button variant="outline" size="sm" className="mt-4" disabled={loading} onClick={() => void load(next)}>Load more</Button>}
    </div>
  )
}

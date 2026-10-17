import { useState } from "react"
import { Link } from "react-router-dom"
import { ShieldCheck, RefreshCw, KeyRound } from "lucide-react"
import { useDataFreshness } from "@tachyon/host-sdk"
import { useApprovals } from "./context"
import { approvalButton } from "./dialog"
import { operatorElevation } from "../operator/elevation"

export function ApprovalsPage() {
  const queue = useApprovals()
  const [view, setView] = useState<"pending" | "history">("pending")
  const [search, setSearch] = useState("")
  const [operatorMessage, setOperatorMessage] = useState("")
  const [unlocking, setUnlocking] = useState(false)
  async function unlock() {
    setUnlocking(true)
    setOperatorMessage("")
    try {
      if (await operatorElevation.ensure())
        setOperatorMessage(
          "Operator access verified. The session lasts up to 12 hours from unlock.",
        )
    } catch (error) {
      setOperatorMessage(
        error instanceof Error
          ? error.message
          : "Could not verify operator access.",
      )
    } finally {
      setUnlocking(false)
    }
  }
  const freshness = useDataFreshness()
    .filter((s) => s.scope === "approvals" && s.url.endsWith("/inbox"))
    .at(-1)
  if (!queue)
    return (
      <p className="p-6 text-sm text-zinc-400">
        Enable Console to view approvals.
      </p>
    )
  const rows = (queue.inbox?.[view] ?? []).filter((item) =>
    `${item.request_snapshot.title} ${item.request_snapshot.source.agent_id} ${item.request_snapshot.source.application_id}`
      .toLowerCase()
      .includes(search.toLowerCase()),
  )
  return (
    <main className="space-y-5 p-4 sm:p-6">
      <header className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <h1 className="flex items-center gap-2 font-mono text-lg text-zinc-200">
            <ShieldCheck className="h-5 w-5 text-amber-300" />
            Approvals
          </h1>
          <p className="mt-1 text-xs text-zinc-500">
            Review requests, inspect evidence, and record decisions.
          </p>
        </div>
        <div className="flex items-center gap-3">
          <button
            className={approvalButton}
            disabled={unlocking}
            onClick={() => void unlock()}
          >
            <KeyRound className="h-3 w-3" /> Operator access
          </button>
          <span className="font-mono text-[10px] text-zinc-500">
            {queue.error ? "Unavailable" : (freshness?.state ?? "Connecting")}
            {freshness?.updatedAt
              ? ` · Updated ${new Date(freshness.updatedAt).toLocaleTimeString()}`
              : ""}
          </span>
          <button
            aria-label="Refresh approvals"
            className={approvalButton}
            onClick={() => void queue.refresh(true)}
          >
            <RefreshCw className="h-3 w-3" />
          </button>
        </div>
      </header>
      {operatorMessage && (
        <p role="status" className="text-xs text-zinc-400">
          {operatorMessage}
        </p>
      )}
      {queue.error && (
        <p
          role="alert"
          className="rounded border border-red-500/30 p-3 text-xs text-red-300"
        >
          Approval queue unavailable: {queue.error}
        </p>
      )}
      <div className="flex flex-wrap justify-between gap-3">
        <div className="flex gap-2">
          {(["pending", "history"] as const).map((v) => (
            <button
              key={v}
              className={`${approvalButton} ${view === v ? "border-sky-500/40 bg-sky-950/30 text-sky-300" : ""}`}
              aria-pressed={view === v}
              onClick={() => setView(v)}
            >
              {v === "pending" ? "Pending" : "History"} ·{" "}
              {queue.inbox?.[v].length ?? 0}
            </button>
          ))}
        </div>
        <input
          aria-label="Search approvals"
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          placeholder="Search requests or agents…"
          className="rounded border border-zinc-700 bg-zinc-950 px-3 py-1.5 text-xs text-zinc-300"
        />
      </div>
      <section
        aria-label={
          view === "pending" ? "Pending approvals" : "Approval history"
        }
        className="divide-y divide-zinc-800/70 border-y border-zinc-800"
      >
        {rows.map((item) => (
          <button
            key={item.item_id}
            onClick={() => queue.open(item.item_id)}
            className="flex w-full items-center justify-between gap-4 px-3 py-3 text-left hover:bg-zinc-900/50 focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-sky-400"
          >
            <div className="min-w-0">
              <div className="truncate text-[13px] text-zinc-200">
                {item.request_snapshot.title}
              </div>
              <div className="mt-1 truncate font-mono text-[10px] text-zinc-500">
                {item.request_snapshot.source.agent_label ??
                  item.request_snapshot.source.agent_id}{" "}
                ·{" "}
                {item.request_snapshot.source.application_label ??
                  item.request_snapshot.source.application_id}{" "}
                · {new Date(item.enqueued_at).toLocaleString()}
              </div>
            </div>
            <span
              className={`shrink-0 font-mono text-[10px] uppercase ${view === "pending" ? "text-amber-300" : "text-zinc-400"}`}
            >
              {item.terminal_outcome?.resolution?.response.decision ??
                item.state}
            </span>
          </button>
        ))}
        {!rows.length && (
          <p className="px-3 py-10 text-center text-sm text-zinc-500">
            {queue.loading
              ? "Loading approvals…"
              : search
                ? "No matching requests."
                : view === "pending"
                  ? "No pending requests. New approval requests will appear here."
                  : "No decision history yet."}
          </p>
        )}
      </section>
    </main>
  )
}
export function ApprovalTrigger({ onNavigate }: { onNavigate?: () => void }) {
  const queue = useApprovals()
  const count = queue?.inbox?.pending.length ?? 0
  return (
    <Link
      to="/approvals"
      onClick={onNavigate}
      aria-label={`Approvals${count ? `, ${count} pending` : ""}`}
      title={
        queue?.error
          ? `Approval queue unavailable: ${queue.error}`
          : "Approvals"
      }
      className="inline-flex items-center gap-1.5 rounded px-2 py-1 text-[11px] text-zinc-400 hover:bg-zinc-800 hover:text-amber-300"
    >
      <ShieldCheck className="h-4 w-4" />
      <span>Approvals</span>
      {count > 0 && (
        <span className="rounded bg-amber-400/15 px-1 text-amber-300">
          {count}
        </span>
      )}
      {queue?.error && <span className="text-red-400">!</span>}
    </Link>
  )
}

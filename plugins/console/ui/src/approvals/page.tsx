import { Button, SearchInput } from "@hollis-labs/design-components"
import { PageHeader } from "@hollis-labs/kit-dashboard"
import { useEffect, useState } from "react"
import { Link, useSearchParams } from "react-router-dom"
import { ShieldCheck, RefreshCw, KeyRound } from "lucide-react"
import { useApprovals } from "./context"
import { DataTable } from "@hollis-labs/kit-dashboard/data"
import { elevationCopy, operatorElevation } from "../operator/elevation"

export function ApprovalsPage() {
  const queue = useApprovals()
  // /approvals?item=<id> (an agent's approval link) opens that request's review dialog.
  const [params, setParams] = useSearchParams()
  const linked = params.get("item")
  const open = queue?.open
  useEffect(() => {
    if (!linked || !open) return
    open(linked)
    setParams((p) => {
      p.delete("item")
      return p
    }, { replace: true })
  }, [linked, open, setParams])
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
          `Operator access verified. ${elevationCopy(await operatorElevation.status())}`,
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
  async function revoke() {
    setUnlocking(true)
    try {
      await operatorElevation.revoke()
      setOperatorMessage("Operator access revoked.")
    } catch (error) {
      setOperatorMessage(error instanceof Error ? error.message : "Could not revoke operator access.")
    } finally {
      setUnlocking(false)
    }
  }
  if (!queue)
    return (
      <div className="flex h-full min-w-0 flex-col">
        <PageHeader title="Approvals" />
        <p className="p-4 dash-support text-fg-muted">Enable Console to view approvals.</p>
      </div>
    )
  const rows = (queue.inbox?.[view] ?? []).filter((item) =>
    `${item.request_snapshot.title} ${item.request_snapshot.source.agent_id} ${item.request_snapshot.source.application_id}`
      .toLowerCase()
      .includes(search.toLowerCase()),
  )
  return (
    <div className="flex h-full min-w-0 flex-col">
      <PageHeader title="Approvals">
        <Button variant="outline" size="sm" className="dash-header-action" disabled={unlocking} onClick={() => void unlock()}>
          <KeyRound className="h-3 w-3" /> Operator access
        </Button>
        <Button variant="outline" size="sm" className="dash-header-action" disabled={unlocking} onClick={() => void revoke()}>
          Revoke elevation
        </Button>
        <Button variant="ghost" size="sm" aria-label="Refresh approvals" onClick={() => void queue.refresh(true)}>
          <RefreshCw className="h-3 w-3" />
        </Button>
      </PageHeader>
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1 border-b border-border px-4 py-2 dash-support text-fg-muted">
        <p>Review requests, inspect evidence, and record decisions.</p>
        <span className="font-mono dash-caption text-fg-faint">
          {queue.error ? "Unavailable" : (queue.inbox ? "Current" : "Connecting")}
          {queue.inbox?.synced_at ? ` · Updated ${new Date(queue.inbox.synced_at).toLocaleTimeString()}` : ""}
        </span>
      </div>
      <div className="min-h-0 flex-1 space-y-5 overflow-auto p-4">
      {operatorMessage && (
        <p role="status" className="dash-control text-fg-muted">
          {operatorMessage}
        </p>
      )}
      {queue.error && (
        <p
          role="alert"
          className="rounded border border-danger/30 p-3 dash-control text-danger"
        >
          Approval queue unavailable: {queue.error}
        </p>
      )}
      <div className="flex flex-wrap justify-between gap-3">
        <div className="flex gap-2">
          {(["pending", "history"] as const).map((v) => (
            <Button variant="outline" size="sm"
              key={v}
              className={`font-mono dash-label ${view === v ? "border-info/40 bg-info-muted/30 text-info" : ""}`}
              aria-pressed={view === v}
              onClick={() => setView(v)}
            >
              {v === "pending" ? "Pending" : "History"} ·{" "}
              {queue.inbox?.[v].length ?? 0}
            </Button>
          ))}
        </div>
        <div className="dash-search-adapter w-72 max-w-full">
          <SearchInput ariaLabel="Search approvals" value={search} onChange={setSearch} placeholder="Search requests or agents…" debounceMs={0} slashToFocus={false} />
        </div>
      </div>
      <section
        aria-label={
          view === "pending" ? "Pending approvals" : "Approval history"
        }
        className="divide-y divide-border/70 border-y border-border"
      >
        <DataTable
          ariaLabel={view === "pending" ? "Pending approvals" : "Approval history"}
          headerVisibility="sr-only"
          density="comfortable"
          items={rows}
          pageSize={Math.max(1, rows.length)}
          getRowId={item => item.item_id}
          onRowOpen={id => queue.open(id)}
          guardedRows
          rowAriaLabel={item => item.request_snapshot.title}
          columns={[
            { key: "request", header: "Request / source", width: "fill", cell: item => <div className="min-w-0">
              <div className="truncate font-sans dash-body text-fg">{item.request_snapshot.title}</div>
              <div className="mt-1 truncate font-mono dash-caption text-fg-faint">
                {item.request_snapshot.source.agent_label ?? item.request_snapshot.source.agent_id} · {item.request_snapshot.source.application_label ?? item.request_snapshot.source.application_id} · {new Date(item.enqueued_at).toLocaleString()}
              </div>
            </div> },
            { key: "state", header: "Decision / state", cell: item => <span className={`font-mono dash-caption uppercase ${view === "pending" ? "text-warning" : "text-fg-muted"}`}>{item.terminal_outcome?.resolution?.response.decision ?? item.state}</span> },
          ]}
        />
        {!rows.length && (
          <p className="px-3 py-10 text-center dash-body text-fg-faint">
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
      <div className="flex gap-2">
        {queue.cursors[`${view}_before`] && <Button variant="outline" size="sm" disabled={queue.loading} onClick={() => void queue.page(view)}>First page</Button>}
        {queue.inbox?.[`next_${view}`] && <Button variant="outline" size="sm" disabled={queue.loading} onClick={() => void queue.page(view, queue.inbox?.[`next_${view}`])}>Next page</Button>}
        <span className="dash-support text-fg-faint">Search applies to this page.</span>
      </div>
      </div>
    </div>
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
      className="inline-flex items-center gap-1.5 rounded px-2 py-1 dash-support text-fg-muted hover:bg-surface-hover hover:text-warning"
    >
      <ShieldCheck className="h-4 w-4" />
      <span>Approvals</span>
      {count > 0 && (
        <span className="rounded bg-warning/15 px-1 text-warning">
          {count}
        </span>
      )}
      {queue?.error && <span className="text-danger">!</span>}
    </Link>
  )
}

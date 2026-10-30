import { Button, SearchInput } from "@hollis-labs/design-components"
import { PageHeader } from "@hollis-labs/kit-dashboard"
import { DashboardViewTabs } from "@hollis-labs/kit-dashboard/layout"
import { useEffect, useState } from "react"
import { Link, useSearchParams } from "react-router-dom"
import { ShieldCheck, RefreshCw, ArrowLeft } from "lucide-react"
import { kindLabel } from "./client"
import { useApprovals } from "./context"
import { ApprovalQueue } from "./queue"
import { DecisionDetail } from "./detail"

export function ApprovalsPage() {
  const queue = useApprovals()
  // /approvals?item=<id> (an agent's approval link) selects that request.
  const [params, setParams] = useSearchParams()
  const linked = params.get("item")
  const [selectedId, setSelectedId] = useState<string | undefined>(linked ?? undefined)
  const [showDetail, setShowDetail] = useState(!!linked)
  useEffect(() => {
    if (!linked) return
    setSelectedId(linked)
    setShowDetail(true)
    setParams((p) => {
      p.delete("item")
      return p
    }, { replace: true })
  }, [linked, setParams])
  const [view, setView] = useState<"pending" | "history">("pending")
  const [search, setSearch] = useState("")
  const restoredItem = queue?.restored
  useEffect(() => {
    if (restoredItem) {
      setSelectedId(restoredItem.itemId)
      setShowDetail(true)
    }
  }, [restoredItem])
  if (!queue)
    return (
      <div className="flex h-full min-w-0 flex-col">
        <PageHeader title="Approvals" />
        <p className="p-4 dash-support text-fg-muted">Enable Console to view approvals.</p>
      </div>
    )
  const pendingCount = queue.inbox?.pending.length ?? 0
  const historyCount = queue.inbox?.history.length ?? 0
  const rows = (queue.inbox?.[view] ?? []).filter((item) =>
    `${item.request_snapshot.title} ${kindLabel(item.request_snapshot.kind)} ${item.request_snapshot.source.agent_id} ${item.request_snapshot.source.application_id}`
      .toLowerCase()
      .includes(search.toLowerCase()),
  )
  // After a decision the row leaves the queue and selection moves to the next item.
  // A held (about to be committed) approval is skipped: selection advances to the next item.
  const heldIds = new Set(queue.held.map((h) => h.itemId))
  const live = rows.filter((r) => !heldIds.has(r.item_id))
  const current = live.find((r) => r.item_id === selectedId) ?? live[0]
  const restored = queue.restored
  const select = (id: string) => {
    setSelectedId(id)
    setShowDetail(true)
  }
  return (
    <div className="flex h-full min-w-0 flex-col">
      <PageHeader title={view === "pending" && pendingCount > 0 ? `Approvals · ${pendingCount} pending` : "Approvals"}>
        <Button variant="ghost" size="sm" aria-label="Refresh approvals" onClick={() => void queue.refresh(true)}>
          <RefreshCw className="h-3 w-3" />
        </Button>
      </PageHeader>
      <div className="flex flex-wrap items-center justify-between gap-3 border-b border-border px-4 py-2">
        {historyCount > 0 && <DashboardViewTabs
          variant="segments"
          label="Approvals view"
          value={view}
          onChange={(v) => setView(v as "pending" | "history")}
          items={[
            { value: "pending", label: "Pending", count: pendingCount },
            // Hidden until there is history to show (the history API lands with #196).
            ...(historyCount > 0 ? [{ value: "history", label: "History", count: historyCount }] : []),
          ]}
        />}
        <div className="dash-search-adapter w-72 max-w-full">
          <SearchInput ariaLabel="Search approvals" value={search} onChange={setSearch} placeholder="Search requests or agents…" debounceMs={0} slashToFocus={false} />
        </div>
      </div>
      {queue.error && (
        <p role="alert" className="mx-4 mt-2 rounded border border-danger/30 p-3 dash-control text-danger">
          Approval queue unavailable: {queue.error}
        </p>
      )}
      <div className="flex min-h-0 flex-1">
        <section
          aria-label={view === "pending" ? "Pending approvals" : "Approval history"}
          className={`min-h-0 w-full shrink-0 overflow-auto border-r border-border md:block md:w-96 ${showDetail && current ? "hidden" : "block"}`}
        >
          <ApprovalQueue
            items={rows}
            selectedId={current?.item_id}
            onSelect={select}
            history={view === "history"}
            held={heldIds}
            empty={
              queue.loading
                ? "Loading approvals…"
                : search
                  ? "No matching requests."
                  : view === "pending"
                    ? "Nothing needs a decision."
                    : "No decision history yet."
            }
          />
          <div className="flex flex-wrap gap-2 p-3">
            {queue.cursors[`${view}_before`] && <Button variant="outline" size="sm" disabled={queue.loading} onClick={() => void queue.page(view)}>First page</Button>}
            {queue.inbox?.[`next_${view}`] && <Button variant="outline" size="sm" disabled={queue.loading} onClick={() => void queue.page(view, queue.inbox?.[`next_${view}`])}>Next page</Button>}
          </div>
        </section>
        <section aria-label="Decision detail" className={`min-h-0 min-w-0 flex-1 md:block ${showDetail && current ? "block" : "hidden"}`}>
          {current ? (
            <>
              <Button variant="ghost" size="sm" className="m-2 md:hidden" onClick={() => setShowDetail(false)}>
                <ArrowLeft className="h-3 w-3" /> Back
              </Button>
              <DecisionDetail key={current.item_id} initialNote={restored?.itemId === current.item_id ? restored.note : ""} id={current.item_id} initial={current} client={queue.client} onChanged={() => void queue.refresh(true)} />
            </>
          ) : (
            <p className="p-6 text-center dash-body text-fg-faint">Select a request to review it.</p>
          )}
        </section>
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

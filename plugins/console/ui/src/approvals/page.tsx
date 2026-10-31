import { Button, DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger, SearchInput } from "@hollis-labs/design-components"
import { PageHeader } from "@hollis-labs/kit-dashboard"
import { PillTabs } from "../components/pill-tabs"
import { useEffect, useState } from "react"
import { Link, useSearchParams } from "react-router-dom"
import { ShieldCheck, RefreshCw, ArrowLeft } from "lucide-react"
import { ApprovalError, kindLabel, type ApprovalItem, type BatchResult, type HistoryFacets } from "./client"
import { Chip } from "../components/chip"
import { BATCH_STATUS_COPY, MAX_BATCH, batchEligibility, batchNeedsAccess, orderPending } from "./batch"
import { useApprovals } from "./context"
import { ApprovalQueue } from "./queue"
import { DecisionDetail } from "./detail"
import { HistoryTimeline } from "./history"
import { BatchPanel } from "./batch-panel"

const STATUSES = [["", "All"], ["approved", "Approved"], ["denied", "Denied"], ["answered", "Answered"], ["expired", "Expired"], ["withdrawn", "Withdrawn"]] as const

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
  const [status, setStatus] = useState("")
  const [requester, setRequester] = useState("")
  const [facets, setFacets] = useState<HistoryFacets>()
  const [checked, setChecked] = useState<Set<string>>(new Set())
  const [confirming, setConfirming] = useState<"approved" | "denied">()
  const [batchBusy, setBatchBusy] = useState(false)
  const [batchNote, setBatchNote] = useState("")
  const [batchResults, setBatchResults] = useState<BatchResult["results"]>()
  const [accessNeeded, setAccessNeeded] = useState(false)
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
  // Requesters we know of: agents on items in the inbox. The server filters by substring, so any of these narrows history.
  const seen = [...new Set((queue.inbox ? [...queue.inbox.pending, ...queue.inbox.history] : []).map((i) => i.request_snapshot.source.agent_label ?? i.request_snapshot.source.agent_id))].sort()
  const requesters = facets?.requesters.length ? facets.requesters.map((r) => r.label) : seen
  const pendingCount = queue.inbox?.pending.length ?? 0
  const historyCount = facets?.statuses.length ? facets.statuses.reduce((n, x) => n + x.count, 0) : undefined
  const facetCount = (v: string) => (v ? facets?.statuses.find((x) => x.status === v)?.count : historyCount)
  const ordered = view === "pending" ? orderPending(queue.inbox?.pending ?? []) : (queue.inbox?.[view] ?? [])
  const rows = ordered.filter((item) =>
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
  const picked = (queue.inbox?.pending ?? []).filter((i: ApprovalItem) => checked.has(i.item_id) && batchEligibility(i).ok).slice(0, MAX_BATCH)
  const lowRisk = (queue.inbox?.pending ?? []).filter((i) => batchEligibility(i).ok && i.request_snapshot.class === "low" && !heldIds.has(i.item_id)).slice(0, MAX_BATCH)
  const batchable = (queue.inbox?.pending ?? []).filter((i: ApprovalItem) => i.batchable && !heldIds.has(i.item_id))
  const check = (id: string, on: boolean) => {
    if (on && checked.size >= MAX_BATCH) return
    setBatchResults(undefined)
    setConfirming(undefined)
    setBatchNote("")
    setChecked((c) => {
      const n = new Set(c)
      if (on) n.add(id)
      else n.delete(id)
      return n
    })
  }
  async function runBatch(decision: "approved" | "denied") {
    setBatchBusy(true)
    try {
      const r = await queue!.client.batchResolve(picked, decision)
      setBatchResults(r.results.filter((x) => x.status !== "resolved"))
      const c = r.counts
      const rest = (c.stale ?? 0) + (c.ineligible ?? 0) + (c.conflict ?? 0) + (c.expired ?? 0) + (c.error ?? 0)
      setBatchNote(
        `${c.resolved ?? 0} ${decision === "approved" ? "approved" : "denied"}${rest ? `, ${rest} left in the queue to review one by one` : ""}.`,
      )
    } catch (e) {
      setBatchNote(
        e instanceof ApprovalError && (e.code === "operator_access_required" || e.code === "assurance_insufficient")
          ? "Batch decisions need elevated operator access."
          : e instanceof Error ? e.message : "The batch did not complete. Nothing was assumed; check the queue.",
      )
    } finally {
      setBatchBusy(false)
      setConfirming(undefined)
      setChecked(new Set())
      void queue!.refresh(true)
    }
  }
  // Ask once when a batch opens; a failed lookup leaves the buttons on, the server still refuses.
  const hasBatch = picked.length > 0
  useEffect(() => {
    if (!hasBatch || !queue) return
    let live = true
    queue.client.access().then((a) => live && setAccessNeeded(batchNeedsAccess(a)), () => {})
    return () => { live = false }
  }, [hasBatch, queue])
  const select = (id: string) => {
    setSelectedId(id)
    setShowDetail(true)
  }
  return (
    <div className="flex h-full min-w-0 flex-col">
      <PageHeader title="Approvals">
        <Button variant="ghost" size="sm" aria-label="Refresh approvals" onClick={() => void queue.refresh(true)}>
          <RefreshCw className="h-3 w-3" />
        </Button>
      </PageHeader>
      <div className="flex flex-wrap items-center justify-between gap-3 border-b border-border px-4 py-2">
        <PillTabs
          label="Approvals view"
          value={view}
          onChange={(v) => setView(v as "pending" | "history")}
          items={[
            { value: "pending", label: "Pending", count: pendingCount },
            { value: "history", label: "History", ...(historyCount !== undefined ? { count: historyCount } : {}) },
          ]}
          className="w-auto"
        />
        <div className="dash-search-adapter w-72 max-w-full">
          <SearchInput ariaLabel="Search approvals" value={search} onChange={setSearch} placeholder="Search requests or agents…" debounceMs={0} />
        </div>
      </div>
      {view === "history" && (
        <div className="flex flex-wrap items-center gap-3 border-b border-border px-4 py-2">
          <PillTabs
            size="sm"
            label="Filter by decision"
            value={status}
            onChange={setStatus}
            items={STATUSES.map(([v, l]) => ({ value: v, label: l, ...(facetCount(v) !== undefined ? { count: facetCount(v) } : {}) }))}
            className="w-auto"
          />
          <DropdownMenu>
            <DropdownMenuTrigger render={<Button type="button" variant="outline" size="sm" aria-label="Filter by requester" />}>
              Requested by: {requester || "anyone"}
            </DropdownMenuTrigger>
            <DropdownMenuContent aria-label="Filter by requester" align="start" data-dashboard-shape="square" className="w-48">
              <DropdownMenuItem onClick={() => setRequester("")}>Anyone</DropdownMenuItem>
              {requesters.map((r) => <DropdownMenuItem key={r} onClick={() => setRequester(r)}>{r}</DropdownMenuItem>)}
            </DropdownMenuContent>
          </DropdownMenu>
          {(status || requester) && <Button type="button" variant="ghost" size="sm" onClick={() => { setStatus(""); setRequester("") }}>Clear filters</Button>}
        </div>
      )}
      {queue.error && (
        <p role="alert" className="mx-4 mt-2 rounded border border-danger/30 p-3 dash-control text-danger">
          Approval queue unavailable: {queue.error}
        </p>
      )}
      {view === "history" ? (
        <HistoryTimeline client={queue.client} search={search} status={status} requester={requester} onOpen={queue.open} onFacets={setFacets} />
      ) : (
      <div className="flex min-h-0 flex-1">
        <section
          aria-label={view === "pending" ? "Pending approvals" : "Approval history"}
          className={`min-h-0 w-full shrink-0 overflow-auto border-r border-border md:block md:w-96 ${showDetail && current ? "hidden" : "block"}`}
        >
          {view === "pending" && (
            <div className="flex items-center justify-between gap-2 px-4 py-2">
              {picked.length > 0 ? (
                <>
                  <span className="dash-control text-fg">{picked.length} selected</span>
                  <Button size="sm" variant="ghost" onClick={() => { setChecked(new Set()); setConfirming(undefined) }}>Clear</Button>
                </>
              ) : (
                <>
                  <h2 className="font-mono dash-caption uppercase tracking-label text-fg-faint">Needs a decision</h2>
                  {lowRisk.length > 0 && <Button size="sm" variant="ghost" className="dash-label" onClick={() => setChecked(new Set(lowRisk.map((i) => i.item_id)))}>Select low risk</Button>}
                </>
              )}
            </div>
          )}
          {view === "pending" && picked.length === 0 && batchNote && (
            <div role="region" aria-label="Batch decision" className="flex items-center gap-2 border-y border-border px-4 py-2 dash-control text-fg-muted">
              <span role="status">{batchNote}</span>
              <Button size="sm" variant="ghost" onClick={() => { setBatchNote(""); setBatchResults(undefined) }}>Dismiss</Button>
            </div>
          )}
          {view === "pending" && picked.length === 0 && batchNote && batchResults && batchResults.length > 0 && (
            <ul aria-label="Batch results" className="divide-y divide-border border-b border-border">
              {batchResults.map((r) => (
                <li key={r.item_id} className="flex items-center gap-3 px-4 py-2">
                  <span className="min-w-0 flex-1 truncate dash-control text-fg">{queue.inbox?.pending.find((i) => i.item_id === r.item_id)?.request_snapshot.title ?? r.item_id}</span>
                  <Chip tone="danger">{BATCH_STATUS_COPY[r.status]}</Chip>
                </li>
              ))}
            </ul>
          )}
          <ApprovalQueue
            checked={checked}
            onCheck={check}
            items={rows}
            selectedId={current?.item_id}
            onSelect={select}
            history={false}
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
          {view === "pending" && picked.length > 0 ? (
            <BatchPanel
              items={picked}
              confirming={confirming}
              busy={batchBusy}
              accessNeeded={accessNeeded}
              onConfirm={(d) => { setBatchNote(""); setConfirming(d) }}
              onCancel={() => setConfirming(undefined)}
              onBack={() => { setConfirming(undefined); setChecked(new Set()) }}
              onRun={(d) => void runBatch(d)}
              onOpen={(id) => { setChecked(new Set()); select(id) }}
            />
          ) : current ? (
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
      )}
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

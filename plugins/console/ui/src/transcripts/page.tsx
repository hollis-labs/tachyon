import { PageHeader } from "@hollis-labs/kit-dashboard"
import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import { Button, SearchInput, Skeleton } from "@hollis-labs/design-components"
import { BarMeter, Kpi, KpiGrid, Panel } from "@hollis-labs/kit-dashboard/widgets"
import { TimeSeriesChart } from "@hollis-labs/kit-dashboard/charts"
import { dataRefreshRegistry } from "@tachyon/host-sdk"
import { Activity, BarChart3, Boxes, FileText, RefreshCw } from "lucide-react"
import { Segmented } from "../components/segmented"
import { TranscriptReader } from "./detail"
import { SessionList, SessionToolbar, WindowPicker } from "./sessions"
import { useSearchStatus } from "./search"
import {
  read,
  filterSessions,
  summarize,
  rank,
  number,
  compact,
  projectName,
  TranscriptError,
  connectSession,
  type Snapshot,
  type Session,
  type Filters,
} from "./data"

type Tab = "sessions" | "insights"

function sinceLabel(at: number, now: number) {
  const m = Math.max(0, Math.floor((now - at) / 60000))
  return m < 1 ? "just now" : m < 60 ? `${m}m ago` : `${Math.floor(m / 60)}h ago`
}

// Deep link: /dashboards/transcripts?session=<id>
const linkedSession = () =>
  new URLSearchParams(window.location.search).get("session") ?? ""

function writeSessionParam(id: string) {
  const url = new URL(window.location.href)
  if (id) url.searchParams.set("session", id)
  else url.searchParams.delete("session")
  window.history.replaceState(window.history.state, "", url)
}

const defaults: Filters = {
  days: 30,
  tool: "",
  model: "",
  kind: "",
  errors: false,
  query: "",
}

function Distribution({
  rows,
  field,
  onSelect,
}: {
  rows: Session[]
  field: "tool" | "model" | "project"
  onSelect(value: string): void
}) {
  const groups = rank(rows, field).slice(0, 7)
  return <div className="p-4">
    <BarMeter rows={groups.map(g => ({ key: g.label, label: field === "project" ? projectName(g.label) : g.label, value: g.value, color: "var(--color-fg-muted)" }))} onSelect={row => onSelect(row.key === "Unspecified" ? "" : row.key)} formatValue={number} showTotal={false} />
    {!groups.length && <p className="py-8 text-center dash-control text-fg-faint">No sessions in this view.</p>}
  </div>
}

export function TranscriptDashboard() {
  const [snapshot, setSnapshot] = useState<Snapshot>()
  const [error, setError] = useState("")
  const [access, setAccess] = useState<"" | "session" | "denied">("")
  const [connecting, setConnecting] = useState(false)
  const [loading, setLoading] = useState(true)
  const [filter, setFilter] = useState<Filters>(defaults)
  const [selected, setSelected] = useState<Session>()
  useSearchStatus()
  const [searchMode, setSearchMode] = useState<"text" | "semantic">("text")
  const [activeId, setActiveId] = useState("")
  const [tab, setTab] = useState<Tab>("sessions")
  const [filteredTo, setFilteredTo] = useState("")
  const [linkedId, setLinkedId] = useState(linkedSession)
  const [loadedAt, setLoadedAt] = useState(0)
  const [now, setNow] = useState(() => new Date().getTime())
  const listPane = useRef<HTMLDivElement>(null)
  const reload = useRef<() => void>(() => {})
  useEffect(() => {
    let active = true,
      sequence = 0
    const load = async () => {
      const n = ++sequence
      try {
        const data = await read<Snapshot>("/snapshot")
        if (active && sequence === n) {
          setSnapshot(data)
          setLoadedAt(new Date().getTime())
          setError("")
          setAccess("")
        }
      } catch (e) {
        if (
          active &&
          sequence === n &&
          e instanceof TranscriptError &&
          (e.status === 401 || e.status === 403 || e.code === "configuration")
        ) {
          setSnapshot(undefined)
          setSelected(undefined)
        }
        if (active && sequence === n) setAccess(e instanceof TranscriptError ? (e.code === "session_required" ? "session" : e.status === 403 ? "denied" : "") : "")
        if (active && sequence === n)
          setError(
            e instanceof Error ? e.message : "Transcript source unavailable.",
          )
      } finally {
        if (active && sequence === n) setLoading(false)
      }
    }
    reload.current = () => void load()
    void load()
    const off = dataRefreshRegistry("transcripts").subscribe(() => {
      void load()
    })
    const timer = setInterval(() => {
      if (document.visibilityState !== "hidden") void load()
    }, 60000)
    const tick = setInterval(() => setNow(new Date().getTime()), 30000)
    return () => {
      clearInterval(tick)
      active = false
      off()
      clearInterval(timer)
    }
  }, [])
  // A deep-linked session opens once the snapshot knows it.
  useEffect(() => {
    if (!linkedId || !snapshot) return
    const hit = snapshot.sessions.find((s) => s.id === linkedId)
    // An id outside the snapshot still opens; the reader fills the header from the page.
    if (hit) setSelected(hit)
    else if (/^[a-f0-9]{64}$/.test(linkedId))
      setSelected({ id: linkedId, title: "", tool: "", kind: "", turns: 0, tokens: 0, tool_calls: 0, errors: 0 })
  }, [linkedId, snapshot])
  const open = (s?: Session) => {
    setSelected(s)
    setLinkedId(s?.id ?? "")
    writeSessionParam(s?.id ?? "")
    if (s) setActiveId(s.id)
  }
  // Closing keeps the list mounted, so scroll and selection are where you left them.
  const close = useCallback(() => {
    open(undefined)
    setTimeout(() => {
      listPane.current
        ?.querySelector<HTMLElement>("[aria-current=true]")
        ?.focus({ preventScroll: true })
    }, 0)
  }, [])
  const rows = useMemo(
    () => filterSessions(snapshot?.sessions ?? [], filter),
    [snapshot, filter],
  )
  const totals = useMemo(() => summarize(rows), [rows])
  const tools = useMemo(() => rank(rows, "tool"), [rows])
  const set = (patch: Partial<Filters>) => {
    setFilter((f) => ({ ...f, ...patch }))
  }
  const reset = () => {
    setFilter(defaults)
    setFilteredTo("")
  }
  // A bar click filters the sessions list and takes you there.
  const drill = (patch: Partial<Filters>, label: string) => {
    set(patch)
    setFilteredTo(label)
    setTab("sessions")
  }
  const facets = snapshot?.facets
  const chartDays = filter.days || 90
  const colors = [
    "var(--color-fg-secondary)",
    "var(--color-fg-muted)",
    "var(--color-fg-faint)",
  ]
  // No snapshot and no access: only the access state, not a healthy-looking page with its own Connect prompts.
  if (!snapshot && error && access)
    return (
      <div className="flex h-full min-w-0 flex-col">
        <PageHeader title="Transcripts" />
        <div role={access === "denied" ? "alert" : "status"} className="flex flex-col items-center gap-2 px-4 py-16 text-center">
          <p className="dash-body font-medium text-fg">{access === "session" ? "Operator access needed" : "Access denied"}</p>
          <p className="max-w-md dash-support text-fg-muted">
            {access === "session"
              ? "Transcripts use the same operator session as Approvals. Connect it to read them."
              : error}
          </p>
          {access === "session" && (
            <Button
              variant="outline"
              size="sm"
              disabled={connecting}
              onClick={() => {
                setConnecting(true)
                void connectSession()
                  .catch(() => undefined)
                  .finally(() => {
                    setConnecting(false)
                    reload.current()
                  })
              }}
            >
              {connecting ? "Connecting" : "Connect"}
            </Button>
          )}
        </div>
      </div>
    )
  return (
    <div className="flex h-full min-w-0 flex-col">
      {/* KIT-GAP: PageHeader has no slot beside the title; the golden puts the Sessions/Insights tabs next to it. */}
      <PageHeader title="Transcripts">
        <Segmented<Tab>
          bare
          label="Transcript views"
          value={tab}
          onChange={setTab}
          items={[
            { value: "sessions", label: "Sessions" },
            { value: "insights", label: "Insights" },
          ]}
        />
        <span role="status" className="ml-auto truncate dash-label text-fg-muted max-md:hidden">
          {snapshot
            ? `Updated ${sinceLabel(loadedAt, now)} · ${number(facets?.total ?? snapshot.sessions.length)} sessions${snapshot.complete ? "" : ` · partial snapshot, latest ${number(snapshot.limit)} maximum`}`
            : "Waiting for metadata"}
        </span>
        <Button variant="outline" size="icon-sm" aria-label="Refresh" className="max-md:ml-auto" onClick={() => reload.current()}>
          <RefreshCw className="size-3.5" />
        </Button>
      </PageHeader>
      <div className={`min-h-0 flex-1 ${tab === "sessions" ? "flex flex-col overflow-hidden" : "space-y-5 overflow-auto p-4"}`}>
      {error && !access && (
        <div role="alert" className={`flex flex-wrap items-center gap-2 rounded-panel border px-3 py-2 dash-control ${tab === "sessions" ? "mx-4 mt-3" : ""} ${snapshot ? "border-border text-fg-muted" : "border-danger/60 text-fg-muted"}`}>
          <span><strong className={snapshot ? "text-fg" : "text-danger"}>Transcript source unavailable.</strong>{" "}{snapshot ? `Showing the last saved snapshot from ${sinceLabel(loadedAt, now)}.` : error}</span>
          <Button variant="outline" size="sm" onClick={() => reload.current()}>
            Retry
          </Button>
        </div>
      )}
      {loading && !snapshot && (
        <div role="status" className="space-y-2 px-4 py-3">
          <p className="dash-control text-fg-muted">
            Building the first metadata snapshot… Later visits use the saved one.
          </p>
          {[0, 1, 2].map((i) => (
            <Skeleton key={i} className="h-10 w-full" />
          ))}
        </div>
      )}
      {tab === "insights" && <section
        aria-label="Database overview"
        className="overflow-hidden rounded border border-border bg-surface/20"
      >
        <div className="border-b border-border px-4 py-2 font-mono dash-caption uppercase tracking-wider text-fg-faint">
          Entire transcript database
        </div>
        <KpiGrid cols="grid-cols-2 lg:grid-cols-4">
          <Kpi
            label="Recorded sessions"
            value={facets ? number(facets.total) : "—"}
            sub="Main sessions and subagents"
          />
          <Kpi
            label="Enrichment coverage"
            value={
              facets
                ? `${facets.total ? Math.round((facets.enriched / facets.total) * 100) : 0}%`
                : "—"
            }
            sub={
              facets
                ? `${number(facets.enriched)} enriched sessions`
                : "Awaiting source"
            }
          />
          <Kpi
            label="Project paths"
            value={facets ? number(facets.project_count) : "—"}
            sub="Distinct recorded paths, including worktrees"
          />
          <Kpi
            label="Tools / models"
            value={
              facets ? `${facets.tools.length} / ${facets.models.length}` : "—"
            }
            sub="Recorded labels, including aliases"
          />
        </KpiGrid>
      </section>}
      {tab === "sessions" ? (
        <div className={`shrink-0 border-b border-border px-4 py-3`}>
          <SessionToolbar filter={filter} facets={facets} set={set} reset={reset} count={rows.length} mode={searchMode} onMode={setSearchMode} />
        </div>
      ) : null}
      <section className={`${tab === "sessions" ? "hidden" : "shrink-0 space-y-4"}`} aria-label="Filtered transcript insights">
        {tab === "insights" && (
          <>
            <WindowPicker filter={filter} set={set} />
            <span className="block dash-control text-fg-muted">{number(rows.length)} sessions in view</span>
          </>
        )}
        {filteredTo && (
          <p className="flex items-center gap-2 dash-control text-fg-muted">
            Filtered to {filteredTo}
            <Button variant="ghost" size="sm" onClick={reset}>
              Reset filters
            </Button>
          </p>
        )}
        {snapshot &&
          snapshot.complete &&
          snapshot.sessions.length !== facets?.total && (
            <p className="dash-control text-warning">
              Snapshot and database totals differ. Records may be undated or may
              have changed during collection; calculations below use loaded
              sessions.
            </p>
          )}
        {tab === "insights" && (
          <>
        <div className="overflow-hidden rounded border border-border bg-surface/20">
          <KpiGrid cols="grid-cols-2 lg:grid-cols-4">
            <Kpi
              label="Conversation turns"
              value={snapshot ? compact(totals.turns) : "—"}
              sub="Within current filters"
            />
            <Kpi
              label="Reported tokens"
              value={snapshot ? compact(totals.tokens) : "—"}
              sub="Includes cached tokens; not a cost estimate"
            />
            <Kpi
              label="Tool calls"
              value={snapshot ? compact(totals.calls) : "—"}
              sub="Recorded invocations"
            />
            <Kpi
              label="Tool errors"
              value={snapshot ? number(totals.errors) : "—"}
              sub={`${number(totals.affected)} sessions affected`}
            />
          </KpiGrid>
        </div>
        <Panel
          title="Session activity"
          icon={<Activity className="h-4 w-4" />}
          meta={
            <span className="dash-caption">
              Last {chartDays} days · UTC · sessions started
            </span>
          }
        >
          <div className="p-4">
            <TimeSeriesChart
              items={rows}
              date={(s) => s.started_at}
              days={chartDays}
              height={210}
              kind="bar"
              series={tools.map((t, i) => ({
                key: `tool${i}`,
                label: t.label,
                color: colors[i % colors.length],
                value: (s: Session) => (s.tool === t.label ? 1 : 0),
              }))}
              emptyLabel="No dated sessions in this time window."
            />
          </div>
        </Panel>
        <div className="grid gap-4 lg:grid-cols-3">
          <Panel
            title="Tools"
            icon={<Boxes className="h-4 w-4" />}
            meta="Sessions"
          >
            <Distribution
              rows={rows}
              field="tool"
              onSelect={(tool) => drill({ tool }, tool || "Unspecified tool")}
            />
          </Panel>
          <Panel
            title="Top models"
            icon={<BarChart3 className="h-4 w-4" />}
            meta="Sessions"
          >
            <Distribution
              rows={rows}
              field="model"
              onSelect={(model) => drill({ model }, model || "Unspecified model")}
            />
          </Panel>
          <Panel
            title="Top project paths"
            icon={<FileText className="h-4 w-4" />}
            meta="Sessions"
          >
            <Distribution
              rows={rows}
              field="project"
              onSelect={(query) => drill({ query }, projectName(query) || "Unspecified project")}
            />
          </Panel>
        </div>
          </>
        )}
      </section>
      {tab === "sessions" && (
        <section aria-label="Transcript sessions" className="flex min-h-0 flex-1">
          <div
            ref={listPane}
            className={`${selected ? "max-md:hidden" : ""} flex min-h-0 w-full flex-col overflow-auto md:w-[26rem] md:shrink-0 md:border-r md:border-border`}
          >
            {!!rows.length && (
              <SessionList
                key={JSON.stringify(filter)}
                rows={rows}
                activeId={activeId}
                onActive={setActiveId}
                onOpen={open}
                keys
              />
            )}
            {!rows.length && !loading && (
              <div className="space-y-3 p-10 text-center dash-body text-fg-faint">
                {!snapshot ? (
                  <>
                    <p>Connect the transcript source to explore sessions.</p>
                    <Button variant="outline" size="sm" onClick={() => reload.current()}>
                      Try again
                    </Button>
                  </>
                ) : !snapshot.sessions.length ? (
                  <p>No transcripts have been recorded yet.</p>
                ) : (
                  <>
                    <p>No sessions match these filters.</p>
                    <Button variant="outline" size="sm" onClick={reset}>
                      Reset filters
                    </Button>
                  </>
                )}
              </div>
            )}
          </div>
          {selected && (
            <TranscriptReader key={selected.id} session={selected} onClose={close} />
          )}
          {!selected && (
            <div className="hidden flex-1 flex-col items-center justify-center gap-2 md:flex">
              <span aria-hidden className="mb-2 flex size-9 items-center justify-center rounded-full border border-border text-fg-muted"><FileText className="size-4" /></span>
              <p className="dash-body font-medium text-fg">Select a session to read it</p>
              <p className="dash-support text-fg-muted">Transcripts are read-only and redacted.</p>
            </div>
          )}
        </section>
      )}
      {tab === "sessions" && <p className="shrink-0 border-t border-border px-4 py-3 dash-label text-fg-muted">Read-only · Fragments Engine owns the records · redacted transcripts</p>}
      </div>
    </div>
  )
}

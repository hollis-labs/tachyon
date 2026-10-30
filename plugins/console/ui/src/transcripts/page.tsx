import { PageHeader } from "@hollis-labs/kit-dashboard"
import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import { Button, SearchInput } from "@hollis-labs/design-components"
import { BarMeter, Kpi, KpiGrid, Panel } from "@hollis-labs/kit-dashboard/widgets"
import { TimeSeriesChart } from "@hollis-labs/kit-dashboard/charts"
import { dataRefreshRegistry } from "@tachyon/host-sdk"
import { Activity, BarChart3, Boxes, FileText, RefreshCw } from "lucide-react"
import { Segmented } from "../components/segmented"
import { TranscriptReader } from "./detail"
import { SessionFilters, SessionList, WindowPicker } from "./sessions"
import {
  read,
  filterSessions,
  summarize,
  rank,
  number,
  compact,
  projectName,
  TranscriptError,
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
  const [loading, setLoading] = useState(true)
  const [filter, setFilter] = useState<Filters>(defaults)
  const [selected, setSelected] = useState<Session>()
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
    if (hit) setSelected(hit)
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
  return (
    <div className="flex h-full min-w-0 flex-col">
      <PageHeader title="Transcripts" />
      <div className="border-b border-border px-4 py-2 dash-support text-fg-muted">
        <p className="font-mono dash-caption text-fg-faint">Dashboards / Fragments Engine</p>
        <p>Explore recorded conversations, usage, and the work behind them. Read-only; Fragments Engine owns the records.</p>
      </div>
      <div className={`min-h-0 flex-1 p-4 ${tab === "sessions" ? "flex flex-col gap-5 overflow-hidden" : "space-y-5 overflow-auto"}`}>
        <div className="flex flex-wrap items-center justify-between gap-2">
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
          <div className="flex items-center gap-2 dash-control text-fg-faint">
            <span role="status">
              {snapshot
                ? `Updated ${sinceLabel(loadedAt, now)} · ${number(snapshot.sessions.length)} sessions loaded${snapshot.complete ? "" : ` · partial snapshot, latest ${number(snapshot.limit)} maximum`}`
                : "Waiting for metadata"}
            </span>
            <Button variant="ghost" size="sm" onClick={() => reload.current()}>
              <RefreshCw className="h-3 w-3" /> Refresh
            </Button>
          </div>
        </div>
      {error && (
        <p
          role="alert"
          className={`dash-control ${snapshot ? "text-fg-muted" : "text-danger"}`}
        >
          Transcript source unavailable: {error}
          {snapshot && " Showing the last snapshot."}{" "}
          <button className="underline" onClick={() => reload.current()}>
            Retry
          </button>
        </p>
      )}
      {loading && (
        <p
          role="status"
          className="rounded border border-border p-6 dash-body text-fg-muted"
        >
          Building the first metadata snapshot… Subsequent visits use the saved
          snapshot.
        </p>
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
      <section className={`shrink-0 space-y-4 ${tab === "sessions" && selected ? "max-md:hidden" : ""}`} aria-label="Filtered transcript insights">
        {tab === "sessions" ? (
          <SessionFilters filter={filter} facets={facets} set={set} reset={reset} />
        ) : (
          <WindowPicker filter={filter} set={set} />
        )}
        <div className="flex flex-wrap items-center justify-between gap-3 dash-control">
          <span className="text-fg-muted">
            {number(rows.length)} sessions in view
          </span>
        </div>
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
        <section aria-label="Transcript sessions" className="-mx-4 -mb-4 flex min-h-0 flex-1 border-t border-border">
          <div
            ref={listPane}
            className={`${selected ? "max-md:hidden" : ""} flex min-h-0 w-full flex-col overflow-auto md:w-[26rem] md:shrink-0 md:border-r md:border-border`}
          >
            <div className="px-4 py-3">
              <SearchInput ariaLabel="Search session metadata" placeholder="Search title, project, or tag…" value={filter.query} onChange={query => set({ query })} debounceMs={0} />
            </div>
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
              <p className="p-10 text-center dash-body text-fg-faint">
                {snapshot
                  ? "No sessions match these filters."
                  : "Connect the transcript source to explore sessions."}
              </p>
            )}
          </div>
          {selected && (
            <TranscriptReader key={selected.id} session={selected} onClose={close} />
          )}
          {!selected && (
            <p className="hidden flex-1 items-center justify-center dash-body text-fg-faint md:flex">
              Select a session to read it.
            </p>
          )}
        </section>
      )}
      </div>
    </div>
  )
}

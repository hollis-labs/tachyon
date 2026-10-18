import { DataTable } from "@hollis-labs/kit-dashboard/data"
import { DashboardToolbar, DashboardField } from "@hollis-labs/kit-dashboard/layout"
import { PageHeader } from "@hollis-labs/kit-dashboard"
import { useEffect, useMemo, useState } from "react"
import { Button, SearchInput } from "@hollis-labs/design-components"
import { BarMeter, Kpi, KpiGrid, Panel } from "@hollis-labs/kit-dashboard/widgets"
import { TimeSeriesChart } from "@hollis-labs/kit-dashboard/charts"
import { dataRefreshRegistry } from "@tachyon/host-sdk"
import {
  Activity,
  BarChart3,
  Boxes,
  FileText,
  Sparkles,
} from "lucide-react"
import { TranscriptDialog } from "./detail"
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
    <BarMeter rows={groups.map(g => ({ key: g.label, label: field === "project" ? projectName(g.label) : g.label, value: g.value, color: "var(--color-info)" }))} onSelect={row => onSelect(row.key === "Unspecified" ? "" : row.key)} formatValue={number} showTotal={false} />
    {!groups.length && <p className="py-8 text-center dash-control text-fg-faint">No sessions in this view.</p>}
  </div>
}

export function TranscriptDashboard() {
  const [snapshot, setSnapshot] = useState<Snapshot>()
  const [error, setError] = useState("")
  const [loading, setLoading] = useState(true)
  const [filter, setFilter] = useState<Filters>(defaults)
  const [page, setPage] = useState(0)
  const [selected, setSelected] = useState<Session>()
  useEffect(() => {
    let active = true,
      sequence = 0
    const load = async () => {
      const n = ++sequence
      try {
        const data = await read<Snapshot>("/snapshot")
        if (active && sequence === n) {
          setSnapshot(data)
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
    void load()
    const off = dataRefreshRegistry("transcripts").subscribe(() => {
      void load()
    })
    const timer = setInterval(() => {
      if (document.visibilityState !== "hidden") void load()
    }, 60000)
    return () => {
      active = false
      off()
      clearInterval(timer)
    }
  }, [])
  const rows = useMemo(
    () => filterSessions(snapshot?.sessions ?? [], filter),
    [snapshot, filter],
  )
  const totals = useMemo(() => summarize(rows), [rows])
  const tools = useMemo(() => rank(rows, "tool"), [rows])
  const set = (patch: Partial<Filters>) => {
    setFilter((f) => ({ ...f, ...patch }))
    setPage(0)
  }
  const maxPage = Math.max(0, Math.ceil(rows.length / 20) - 1)
  const visiblePage = Math.min(page, maxPage)
  const facets = snapshot?.facets
  const chartDays = filter.days || 90
  const colors = [
    "var(--color-dash-series-1)",
    "var(--color-dash-series-2)",
    "var(--color-dash-series-3)",
  ]
  return (
    <div className="flex h-full min-w-0 flex-col">
      <PageHeader title="Transcript insights" />
      <div className="border-b border-border px-4 py-2 dash-support text-fg-muted">
        <p className="font-mono dash-caption text-fg-faint">Dashboards / Fragments Engine</p>
        <p>Explore recorded conversations, usage, and the work behind them.</p>
      </div>
      <div className="min-h-0 flex-1 space-y-5 overflow-auto p-4">
        {snapshot && (
          <div className="rounded border border-border px-3 py-2 text-right">
            <div className="flex items-center gap-2 font-mono dash-caption uppercase text-info">
              <Sparkles className="h-3 w-3" />
              {snapshot.status.semantic
                ? "Semantic index available"
                : "Text index available"}
            </div>
            <p className="mt-1 dash-support text-fg-faint">
              Read-only · Fragments Engine owns the records
            </p>
          </div>
        )}
      {error && (
        <div
          role="alert"
          className="rounded border border-danger/30 bg-danger-muted/10 p-3 dash-control text-danger"
        >
          Transcript source unavailable: {error}
        </div>
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
      <section
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
      </section>
      <section className="space-y-4" aria-label="Filtered transcript insights">
        <DashboardToolbar className="items-end">
          <DashboardField orientation="stacked" label="Time window">
            <select
              aria-label="Time window"
              className="max-w-64"
              value={filter.days}
              onChange={(e) => set({ days: Number(e.target.value) })}
            >
              <option value={7}>Last 7 days</option>
              <option value={30}>Last 30 days</option>
              <option value={90}>Last 90 days</option>
              <option value={0}>All recorded time</option>
            </select>
          </DashboardField>
          <DashboardField orientation="stacked" label="Tool">
            <select
              aria-label="Tool"
              className="max-w-64"
              value={filter.tool}
              onChange={(e) => set({ tool: e.target.value })}
            >
              <option value="">All tools</option>
              {facets?.tools.map((t) => (
                <option key={t}>{t}</option>
              ))}
            </select>
          </DashboardField>
          <DashboardField orientation="stacked" label="Model">
            <select
              aria-label="Model"
              className="max-w-56"
              value={filter.model}
              onChange={(e) => set({ model: e.target.value })}
            >
              <option value="">All models</option>
              {facets?.models.map((t) => (
                <option key={t}>{t}</option>
              ))}
            </select>
          </DashboardField>
          <DashboardField orientation="stacked" label="Session kind">
            <select
              aria-label="Session kind"
              className="max-w-64"
              value={filter.kind}
              onChange={(e) => set({ kind: e.target.value })}
            >
              <option value="">All kinds</option>
              <option value="main">Main sessions</option>
              <option value="subagent">Subagents</option>
            </select>
          </DashboardField>
          <label className="flex h-8 items-center gap-2 px-2 dash-control text-fg-muted">
            <input
              type="checkbox"
              checked={filter.errors}
              onChange={(e) => set({ errors: e.target.checked })}
            />
            With tool errors
          </label>
          <Button
            variant="ghost"
            size="sm"
            onClick={() => {
              setFilter(defaults)
              setPage(0)
            }}
          >
            Reset
          </Button>
        </DashboardToolbar>
        <div className="flex flex-wrap items-center justify-between gap-3 dash-control">
          <span className="text-fg-muted">
            {number(rows.length)} sessions in view{" "}
            <span className="text-fg-faint">· filtered metadata</span>
          </span>
          <span
            className={snapshot?.complete ? "text-fg-faint" : "text-warning"}
          >
            {snapshot
              ? `${number(snapshot.sessions.length)} sessions loaded · ${snapshot.complete ? "source pagination complete" : `partial snapshot, latest ${number(snapshot.limit)} maximum`}`
              : "Waiting for metadata"}
          </span>
        </div>
        {snapshot &&
          snapshot.complete &&
          snapshot.sessions.length !== facets?.total && (
            <p className="dash-control text-warning">
              Snapshot and database totals differ. Records may be undated or may
              have changed during collection; calculations below use loaded
              sessions.
            </p>
          )}
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
              onSelect={(tool) => set({ tool })}
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
              onSelect={(model) => set({ model })}
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
              onSelect={(query) => set({ query })}
            />
          </Panel>
        </div>
      </section>
      <section
        className="overflow-hidden rounded border border-border"
        aria-label="Transcript sessions"
      >
        <div className="flex flex-wrap items-center justify-between gap-3 border-b border-border p-4">
          <h2 className="font-mono dash-control uppercase tracking-wide text-fg-muted">
            Explore sessions
          </h2>
          <div className="dash-search-adapter w-64 max-w-full">
            <SearchInput ariaLabel="Search session metadata" placeholder="Search title, project, or tag…" value={filter.query} onChange={query => set({ query })} debounceMs={0} slashToFocus={false} />
          </div>
        </div>
        <DataTable
          ariaLabel="Transcript sessions"
          className="min-w-3xl"
          density="comfortable"
          items={rows.slice(visiblePage * 20, (visiblePage + 1) * 20)}
          getRowId={s => s.id}
          columns={[
            { key: "session", header: "Session / project", width: "fill", cell: s => <div className="min-w-0">
              <button onClick={() => setSelected(s)} className="block w-full truncate text-left dash-body text-fg hover:text-info" title={s.title}>{s.title || "Untitled session"}</button>
              <div className="mt-1 truncate dash-caption text-fg-faint" title={s.project}>{projectName(s.project)} · {s.kind}</div>
            </div> },
            { key: "tool", header: "Tool / model", cell: s => <><span className="dash-control text-info">{s.tool}</span><div className="mt-1 max-w-52 truncate font-mono dash-caption text-fg-faint">{s.model || "Unspecified"}</div></> },
            { key: "started", header: "Started", cell: s => <span className="dash-control text-fg-muted">{s.started_at ? new Date(s.started_at).toLocaleDateString() : "Unknown"}</span> },
            { key: "turns", header: "Turns", cell: s => <span className="dash-control text-fg-muted">{number(s.turns)}</span> },
            { key: "tokens", header: "Tokens", cell: s => <span className="dash-control text-fg-muted" title={number(s.tokens)}>{compact(s.tokens)}</span> },
            { key: "errors", header: "Errors", cell: s => <span className={`dash-control ${s.errors ? "text-warning" : "text-fg-faint"}`}>{number(s.errors)}</span> },
          ]}
        />
        {!rows.length && !loading && (
          <p className="p-10 text-center dash-body text-fg-faint">
            {snapshot
              ? "No sessions match these filters."
              : "Connect the transcript source to explore sessions."}
          </p>
        )}
        <div className="flex items-center justify-between border-t border-border px-4 py-3 dash-control text-fg-faint">
          <span>
            {rows.length
              ? `${visiblePage * 20 + 1}–${Math.min(rows.length, (visiblePage + 1) * 20)} of ${number(rows.length)}`
              : "0 sessions"}
          </span>
          <div className="flex gap-2">
            <Button
              variant="ghost"
              size="sm"
              disabled={!visiblePage}
              onClick={() => setPage(visiblePage - 1)}
            >
              Previous
            </Button>
            <Button
              variant="ghost"
              size="sm"
              disabled={visiblePage >= maxPage}
              onClick={() => setPage(visiblePage + 1)}
            >
              Next
            </Button>
          </div>
        </div>
      </section>
      {selected && (
        <TranscriptDialog
          key={selected.id}
          session={selected}
          onClose={() => setSelected(undefined)}
        />
      )}
      </div>
    </div>
  )
}

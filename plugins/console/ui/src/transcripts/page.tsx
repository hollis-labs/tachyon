import { useEffect, useMemo, useState } from "react"
import { Button } from "@hollis-labs/design-components"
import { Kpi, KpiGrid, Panel } from "@hollis-labs/kit-dashboard/widgets"
import { TimeSeriesChart } from "@hollis-labs/kit-dashboard/charts"
import { dataRefreshRegistry } from "@tachyon/host-sdk"
import {
  Activity,
  BarChart3,
  Boxes,
  Database,
  FileText,
  Search,
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

const input =
  "rounded border border-zinc-700 bg-zinc-950 px-3 py-2 text-xs text-zinc-300 focus:border-sky-500 focus:outline-none"
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
  return (
    <div className="space-y-3 p-4">
      {groups.map((g) => (
        <button
          key={g.label}
          title={g.label}
          onClick={() => onSelect(g.label === "Unspecified" ? "" : g.label)}
          className="block w-full rounded text-left hover:bg-zinc-800/40 focus-visible:outline focus-visible:outline-sky-400"
        >
          <div className="mb-1 flex justify-between gap-3 text-xs">
            <span className="truncate text-zinc-300">
              {field === "project" ? projectName(g.label) : g.label}
            </span>
            <span className="font-mono text-zinc-500">{number(g.value)}</span>
          </div>
          <div className="h-1 bg-zinc-800">
            <div
              className="h-full bg-sky-400/60"
              style={{
                width: `${(g.value / Math.max(1, groups[0].value)) * 100}%`,
              }}
            />
          </div>
        </button>
      ))}
      {!groups.length && (
        <p className="py-8 text-center text-xs text-zinc-500">
          No sessions in this view.
        </p>
      )}
    </div>
  )
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
    "var(--color-dash-status-done, #38bdf8)",
    "var(--color-dash-status-active, #a78bfa)",
    "var(--color-dash-status-blocked, #fbbf24)",
  ]
  return (
    <main className="space-y-5 p-4 sm:p-6">
      <header className="flex flex-wrap items-start justify-between gap-4">
        <div>
          <p className="mb-2 font-mono text-[10px] uppercase tracking-[0.2em] text-sky-400">
            Dashboards / Fragments Engine
          </p>
          <h1 className="flex items-center gap-2 font-mono text-xl text-zinc-200">
            <Database className="h-5 w-5 text-sky-300" /> Transcript insights
          </h1>
          <p className="mt-2 text-xs text-zinc-500">
            Explore recorded conversations, usage, and the work behind them.
          </p>
        </div>
        {snapshot && (
          <div className="rounded border border-zinc-800 px-3 py-2 text-right">
            <div className="flex items-center gap-2 font-mono text-[10px] uppercase text-sky-300">
              <Sparkles className="h-3 w-3" />
              {snapshot.status.semantic
                ? "Semantic index available"
                : "Text index available"}
            </div>
            <p className="mt-1 text-[11px] text-zinc-500">
              Read-only · Fragments Engine owns the records
            </p>
          </div>
        )}
      </header>
      {error && (
        <div
          role="alert"
          className="rounded border border-red-500/30 bg-red-950/10 p-3 text-xs text-red-300"
        >
          Transcript source unavailable: {error}
        </div>
      )}
      {loading && (
        <p
          role="status"
          className="rounded border border-zinc-800 p-6 text-sm text-zinc-400"
        >
          Building the first metadata snapshot… Subsequent visits use the saved
          snapshot.
        </p>
      )}
      <section
        aria-label="Database overview"
        className="overflow-hidden rounded border border-zinc-800 bg-zinc-900/20"
      >
        <div className="border-b border-zinc-800 px-4 py-2 font-mono text-[10px] uppercase tracking-wider text-zinc-500">
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
        <div className="flex flex-wrap items-end gap-2">
          <label className="space-y-1 text-[10px] text-zinc-500">
            <span className="block">Time window</span>
            <select
              aria-label="Time window"
              className={input}
              value={filter.days}
              onChange={(e) => set({ days: Number(e.target.value) })}
            >
              <option value={7}>Last 7 days</option>
              <option value={30}>Last 30 days</option>
              <option value={90}>Last 90 days</option>
              <option value={0}>All recorded time</option>
            </select>
          </label>
          <label className="space-y-1 text-[10px] text-zinc-500">
            <span className="block">Tool</span>
            <select
              aria-label="Tool"
              className={input}
              value={filter.tool}
              onChange={(e) => set({ tool: e.target.value })}
            >
              <option value="">All tools</option>
              {facets?.tools.map((t) => (
                <option key={t}>{t}</option>
              ))}
            </select>
          </label>
          <label className="space-y-1 text-[10px] text-zinc-500">
            <span className="block">Model</span>
            <select
              aria-label="Model"
              className={`${input} max-w-[220px]`}
              value={filter.model}
              onChange={(e) => set({ model: e.target.value })}
            >
              <option value="">All models</option>
              {facets?.models.map((t) => (
                <option key={t}>{t}</option>
              ))}
            </select>
          </label>
          <label className="space-y-1 text-[10px] text-zinc-500">
            <span className="block">Session kind</span>
            <select
              aria-label="Session kind"
              className={input}
              value={filter.kind}
              onChange={(e) => set({ kind: e.target.value })}
            >
              <option value="">All kinds</option>
              <option value="main">Main sessions</option>
              <option value="subagent">Subagents</option>
            </select>
          </label>
          <label className="flex h-[34px] items-center gap-2 px-2 text-xs text-zinc-400">
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
        </div>
        <div className="flex flex-wrap items-center justify-between gap-3 text-xs">
          <span className="text-zinc-400">
            {number(rows.length)} sessions in view{" "}
            <span className="text-zinc-600">· filtered metadata</span>
          </span>
          <span
            className={snapshot?.complete ? "text-zinc-500" : "text-amber-300"}
          >
            {snapshot
              ? `${number(snapshot.sessions.length)} sessions loaded · ${snapshot.complete ? "source pagination complete" : `partial snapshot, latest ${number(snapshot.limit)} maximum`}`
              : "Waiting for metadata"}
          </span>
        </div>
        {snapshot &&
          snapshot.complete &&
          snapshot.sessions.length !== facets?.total && (
            <p className="text-xs text-amber-300">
              Snapshot and database totals differ. Records may be undated or may
              have changed during collection; calculations below use loaded
              sessions.
            </p>
          )}
        <div className="overflow-hidden rounded border border-zinc-800 bg-zinc-900/20">
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
            <span className="text-[10px]">
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
        className="overflow-hidden rounded border border-zinc-800"
        aria-label="Transcript sessions"
      >
        <div className="flex flex-wrap items-center justify-between gap-3 border-b border-zinc-800 p-4">
          <h2 className="font-mono text-xs uppercase tracking-wide text-zinc-300">
            Explore sessions
          </h2>
          <label className="flex items-center gap-2">
            <Search className="h-4 w-4 text-zinc-500" />
            <input
              aria-label="Search session metadata"
              placeholder="Search title, project, or tag…"
              className={`${input} w-64 max-w-full`}
              value={filter.query}
              onChange={(e) => set({ query: e.target.value })}
            />
          </label>
        </div>
        <div className="overflow-x-auto">
          <table className="w-full min-w-[760px] text-left text-xs">
            <thead className="border-b border-zinc-800 font-mono text-[10px] uppercase text-zinc-500">
              <tr>
                {[
                  "Session / project",
                  "Tool / model",
                  "Started",
                  "Turns",
                  "Tokens",
                  "Errors",
                ].map((h) => (
                  <th key={h} className="px-4 py-3 font-normal">
                    {h}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody className="divide-y divide-zinc-800/60">
              {rows.slice(visiblePage * 20, (visiblePage + 1) * 20).map((s) => (
                <tr key={s.id} className="hover:bg-zinc-900/60">
                  <td className="max-w-[330px] px-4 py-3">
                    <button
                      onClick={() => setSelected(s)}
                      className="block w-full truncate text-left text-[13px] text-zinc-200 hover:text-sky-300"
                      title={s.title}
                    >
                      {s.title || "Untitled session"}
                    </button>
                    <div
                      className="mt-1 truncate text-[10px] text-zinc-500"
                      title={s.project}
                    >
                      {projectName(s.project)} · {s.kind}
                    </div>
                  </td>
                  <td className="px-4 py-3">
                    <span className="text-sky-300">{s.tool}</span>
                    <div className="mt-1 max-w-[200px] truncate font-mono text-[10px] text-zinc-500">
                      {s.model || "Unspecified"}
                    </div>
                  </td>
                  <td className="whitespace-nowrap px-4 py-3 text-zinc-400">
                    {s.started_at
                      ? new Date(s.started_at).toLocaleDateString()
                      : "Unknown"}
                  </td>
                  <td className="px-4 py-3 font-mono text-zinc-400">
                    {number(s.turns)}
                  </td>
                  <td
                    className="px-4 py-3 font-mono text-zinc-400"
                    title={number(s.tokens)}
                  >
                    {compact(s.tokens)}
                  </td>
                  <td
                    className={`px-4 py-3 font-mono ${s.errors ? "text-amber-300" : "text-zinc-600"}`}
                  >
                    {number(s.errors)}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
        {!rows.length && !loading && (
          <p className="p-10 text-center text-sm text-zinc-500">
            {snapshot
              ? "No sessions match these filters."
              : "Connect the transcript source to explore sessions."}
          </p>
        )}
        <div className="flex items-center justify-between border-t border-zinc-800 px-4 py-3 text-xs text-zinc-500">
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
    </main>
  )
}

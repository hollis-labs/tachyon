import { useState, type ReactNode } from 'react'
import { Link } from 'react-router-dom'
import { SearchInput, Skeleton, cn } from '@hollis-labs/design-components'
import { Chip, SegmentedProgress } from '@planner/shared'
import { useAgentsRead } from './api'
import { ContextBar } from './session-rows'
import { Cite, EventFeed, toTimeline, type Entry, type EventsResponse } from './session-events'
import { ageText } from './session-parts'
import { AGENTS } from './vocabulary'
import type { Session } from './types'

const M = AGENTS.modal
const heading = 'font-mono dash-caption uppercase tracking-label text-fg-faint'

const fmt = (n: number | null | undefined) => (n === null || n === undefined ? M.unknown : n.toLocaleString())
function Panel<T>({ read, children }: { read: { data: T | null; error: string | null; reload: () => void }; children: (d: T) => ReactNode }) {
  if (read.error) return <p role="alert" className="dash-support text-danger">{M.loadFailed}: {read.error} <button type="button" className="underline" onClick={() => void read.reload()}>{M.retry}</button></p>
  if (read.data === null) return <div aria-busy className="flex flex-col gap-2">{[0, 1, 2].map((i) => <Skeleton key={i} className="h-8 w-full" />)}</div>
  return <>{children(read.data)}</>
}

function Row({ label, value }: { label: string; value: ReactNode }) {
  return <div className="flex justify-between gap-4 border-b border-border-subtle py-1.5 dash-support"><dt className="text-fg-muted">{label}</dt><dd className="text-fg">{value}</dd></div>
}

// ---------- Now ----------

const caption = 'font-mono text-caption uppercase tracking-label text-fg-muted'
const themeId = (ref: string) => ref.replace(/^.*theme[:/]/, '')

function Kpi({ label, value, title }: { label: string; value: ReactNode; title?: string }) {
  return (
    <div title={title} className="flex flex-col gap-0.5 bg-bg-elevated px-3 py-2.5">
      <small className={caption}>{label}</small>
      <b className="font-mono text-base font-semibold text-fg">{value}</b>
    </div>
  )
}

/** The six facts every tab opens with. Unknown is n/a or not metered with the reason on hover, inferred values carry "~". */
export function KpiStrip({ s, now }: { s: Session; now: number }) {
  const na = { value: M.na, title: AGENTS.naReason }
  const tokens = s.tokens.metered ? { value: fmt((s.tokens.input ?? 0) + (s.tokens.output ?? 0)), title: undefined } : { value: M.unmetered, title: M.unmeteredReason }
  const clear = s.last_clear_at ? { value: `${s.last_clear_at.inferred ? '~' : ''}${ageText(now - s.last_clear_at.at)} ago`, title: s.last_clear_at.inferred ? M.inferred : undefined } : { value: M.never, title: undefined }
  // 0 with nothing metered or seen is "not collected", not zero calls.
  const noCalls = s.tool_call_count === null || (s.tool_call_count === 0 && !s.tokens.metered && !s.one_liner)
  const kpis: [string, ReactNode, string | undefined][] = [
    [M.kpi.state, s.state === 'working' && s.state_inferred ? `~${AGENTS.states.working}` : AGENTS.states[s.state], s.state === 'working' && s.state_inferred ? M.inferred : undefined],
    [M.kpi.runtime, s.runtime_ms > 0 ? ageText(s.runtime_ms) : na.value, s.runtime_ms > 0 ? undefined : na.title],
    [M.kpi.context, s.context.pct === null ? na.value : `${s.context.pct}%`, s.context.pct === null ? AGENTS.contextNa : undefined],
    [M.kpi.tokens, tokens.value, tokens.title],
    [M.kpi.calls, noCalls ? na.value : fmt(s.tool_call_count), noCalls ? M.callsNotCollected : undefined],
    [M.kpi.lastClear, clear.value, clear.title],
  ]
  return (
    <div className="grid grid-cols-2 gap-px overflow-hidden rounded-control border border-border-subtle bg-border-subtle sm:grid-cols-3 lg:grid-cols-6">
      {kpis.map(([k, v, t]) => <Kpi key={k} label={k} value={v} title={t} />)}
    </div>
  )
}

export function NowTab({ s, now }: { s: Session; now: number }) {
  const latest = useAgentsRead<EventsResponse>(`/sessions/${encodeURIComponent(s.id)}/events?group_runs=1&limit=8`)
  const a = s.assignment
  const hasAssignment = !!a && !!(a.task_ref || a.theme_ref)
  const st = a?.stats
  return (
    <div className="flex flex-col gap-4">
      <KpiStrip s={s} now={now} />
      <div className={cn('grid items-start gap-3.5', hasAssignment && 'lg:grid-cols-[1fr_320px]')}>
        <section aria-labelledby="now-right" className="flex flex-col gap-1.5">
          <h3 id="now-right" className={caption}>{M.rightNow}</h3>
          {s.one_liner ? (
            <>
              <span className="text-control text-fg">{s.one_liner.verb} <Cite>{s.one_liner.target}</Cite> <span className="text-fg-muted">· {ageText(now - s.one_liner.at)}</span></span>
              <span className="text-xs text-fg-muted">{M.callsThisTurn(s.one_liner.calls_this_turn)} · <Cite>{s.one_liner.tool}</Cite></span>
            </>
          ) : <p className="text-control text-fg-muted">{M.nothingNow}</p>}
        </section>
        {hasAssignment && a && (
          <section aria-labelledby="now-assigned" className="flex flex-col gap-2 rounded-panel border border-border bg-bg p-3">
            <h3 id="now-assigned" className={caption}>{M.assigned}</h3>
            {a.theme_ref && (
              <span className="text-label text-fg-muted">{M.themeLink} · <Link to={`/planner/themes/${encodeURIComponent(themeId(a.theme_ref))}`}><Cite>{themeId(a.theme_ref)} ↗</Cite></Link></span>
            )}
            {a.task_ref && <span className="text-control font-semibold text-fg">{a.task_ref}</span>}
            {a.index != null && a.count != null && <span className="text-xs text-fg-muted">{M.taskOf(a.index, a.count)}</span>}
            {a.index != null && a.count != null && <SegmentedProgress size="md" label={M.taskOf(a.index, a.count)} items={[{ state: 'done', count: st?.done ?? Math.max(0, a.index - 1) }, { state: 'doing', count: 1 }, { state: 'todo', count: Math.max(0, a.count - (st?.done ?? a.index - 1) - 1) }]} />}
            {st && (
              <div className="grid grid-cols-3 gap-2">
                {a.count != null && <div className="flex flex-col"><b className="font-mono text-sm font-semibold text-fg">{st.done}/{a.count}</b><small className={caption}>{M.done}</small></div>}
                {st.time_on_task_ms != null && <div className="flex flex-col"><b className="font-mono text-sm font-semibold text-fg">{ageText(st.time_on_task_ms)}</b><small className={caption}>{M.onTask}</small></div>}
                {st.events != null && <div className="flex flex-col"><b className="font-mono text-sm font-semibold text-fg">{st.events}</b><small className={caption}>{M.events}</small></div>}
              </div>
            )}
          </section>
        )}
      </div>
      <div className="grid items-stretch gap-3.5 md:grid-cols-2">
        <section aria-labelledby="now-context" className="flex flex-col gap-2 rounded-panel border border-border p-3">
          <h3 id="now-context" className={caption}>{M.contextTitle}{s.context.pct !== null && ` · ${s.context.pct}% of window`}</h3>
          <ContextBar pct={s.context.pct} />
          <p className="text-xs text-fg-muted">{s.context.used !== null && s.context.window !== null ? `${M.ctxWindow(fmt(s.context.used), fmt(s.context.window))}. ` : ''}{M.contextTotalOnly}</p>
        </section>
        <section aria-labelledby="now-health" className="flex flex-col gap-2 rounded-panel border border-border p-3">
          <h3 id="now-health" className={caption}>{M.health}</h3>
          <dl className="grid grid-cols-2 text-xs">
            {([
              [M.provider, `${s.provider}${s.model ? ` · ${s.model}` : ''}`],
              [M.lastActivity, s.last_activity_at ? `${ageText(now - s.last_activity_at)}` : M.na],
              [M.pendingApproval, s.pending.known ? (s.pending.approval ? M.yes : M.no) : M.unknown],
              [M.pendingQuestion, s.pending.known ? (s.pending.question ? M.yes : M.no) : M.unknown],
            ] as const).map(([k, v]) => (
              <div key={k} className="contents"><dt className="border-b border-border-subtle py-2 text-fg-secondary">{k}</dt><dd className="border-b border-border-subtle py-2 text-fg">{v}</dd></div>
            ))}
          </dl>
        </section>
      </div>
      <section aria-labelledby="now-latest" className="flex flex-col gap-1">
        <h3 id="now-latest" className={caption}>{M.latest}</h3>
        <Panel read={latest}>{(d) => (toTimeline(d, now).length === 0 ? <p className="text-xs text-fg-muted">{M.latestEmpty}</p> : <EventFeed entries={toTimeline(d, now)} now={now} label={M.latest} />)}</Panel>
      </section>
    </div>
  )
}

// ---------- Timeline ----------

export function TimelineTab({ s, now }: { s: Session; now: number }) {
  const read = useAgentsRead<EventsResponse>(`/sessions/${encodeURIComponent(s.id)}/events?group_runs=1&limit=100`)
  const [q, setQ] = useState('')
  const [facet, setFacet] = useState('all')
  return (
    <Panel read={read}>
      {(d) => {
        const all = toTimeline(d, now)
        const callCount = (e: Entry) => (e.type === 'tool_run' ? e.calls ?? 1 : 1)
        const count = (f: string) => all.filter((e) => !e.gap && (f === 'all' || e.facet === f)).reduce((n, e) => n + callCount(e), 0)
        const needle = q.trim().toLowerCase()
        const shown = all.filter((e) => (facet === 'all' || e.facet === facet) && (!needle || e.summary.toLowerCase().includes(needle)))
        return (
          <div className="flex flex-col gap-3">
            <div className="flex flex-wrap items-center gap-2">
              <div className="min-w-48 flex-1"><SearchInput value={q} onChange={setQ} placeholder={M.searchEvents} ariaLabel={M.searchEvents} slashToFocus={false} /></div>
              <div role="toolbar" aria-label={M.timelineFilters} className="flex flex-wrap items-center gap-1.5">
                {(Object.keys(M.facet) as (keyof typeof M.facet)[]).map((f) => (
                  <button key={f} type="button" aria-pressed={facet === f} onClick={() => setFacet(f)} className="rounded-full">
                    <Chip tone={f === 'errors' && count('errors') > 0 ? 'danger' : 'neutral'} selected={facet === f} className="h-6 cursor-pointer hover:bg-selection">{M.facet[f]} <span className="font-mono tabular-nums">{count(f)}</span></Chip>
                  </button>
                ))}
              </div>
            </div>
            {shown.length === 0 ? <p className="text-xs text-fg-muted">{all.length === 0 ? M.timelineEmpty : M.timelineNoMatch}</p> : <EventFeed entries={shown} now={now} limit={12} label={M.timelineStream} />}
          </div>
        )
      }}
    </Panel>
  )
}

// ---------- Context ----------

interface Series { points: { at: number; pct: number }[]; compactions: { at: number; before?: number; after?: number; inferred: boolean }[]; clears: { at: number; inferred: boolean }[]; warn_pct: number }
interface Tokens { metered: boolean; hour: Record<string, number> | null; session: Record<string, number> | null; share: number | null; trend: 'up' | 'down' | 'flat' | null }

const compact = (n: number | null | undefined) => {
  if (n === null || n === undefined) return M.unknown
  if (n >= 1e6) return `${+(n / 1e6).toFixed(1)}M`
  if (n >= 1e3) return `${Math.round(n / 1e3)}K`
  return String(n)
}

const CHART_H = 110

/** Context use over time: the line, a dashed warning line, and a dot where context was compacted (hollow when inferred). */
function ContextChart({ d }: { d: Series }) {
  d = { ...d, compactions: d.compactions ?? [], clears: d.clears ?? [] }
  const t0 = d.points[0].at
  const span = Math.max(1, d.points[d.points.length - 1].at - t0)
  const x = (at: number) => ((at - t0) / span) * 100
  const y = (pct: number) => CHART_H - (Math.min(100, pct) / 100) * CHART_H
  const pts = d.points.map((p) => `${(x(p.at) * 6).toFixed(1)},${y(p.pct).toFixed(1)}`).join(' ')
  // The dot sits on the line: the reading at the moment of compaction, interpolated between the two points around it.
  const pctAt = (t: number) => {
    const i = d.points.findIndex((p) => p.at >= t)
    if (i <= 0) return d.points[Math.max(0, i)].pct
    const [a, b] = [d.points[i - 1], d.points[i]]
    return a.pct + ((b.pct - a.pct) * (t - a.at)) / Math.max(1, b.at - a.at)
  }
  return (
    <div className="relative" style={{ height: CHART_H }}>
      <svg viewBox={`0 0 600 ${CHART_H}`} preserveAspectRatio="none" role="img" aria-label={M.contextOverTime} className="size-full overflow-visible text-fg">
        <line x1="0" x2="600" y1={y(d.warn_pct)} y2={y(d.warn_pct)} stroke="currentColor" strokeDasharray="4 4" className="text-danger" opacity="0.6" vectorEffect="non-scaling-stroke" />
        <polyline fill="none" stroke="currentColor" strokeWidth="1.5" points={pts} vectorEffect="non-scaling-stroke" />
      </svg>
      {(d.compactions ?? []).map((c) => (
        <i key={c.at} aria-hidden title={c.inferred ? M.inferred : undefined} className={cn('absolute size-1.5 -translate-x-1/2 -translate-y-1/2 rounded-full border border-fg', c.inferred ? 'bg-bg-elevated' : 'bg-fg')} style={{ left: `${x(c.at)}%`, top: y(pctAt(c.at)) }} />
      ))}
    </div>
  )
}

export function ContextTab({ s, now }: { s: Session; now: number }) {
  const series = useAgentsRead<Series>(`/sessions/${encodeURIComponent(s.id)}/context-series`)
  const tokens = useAgentsRead<Tokens>(`/sessions/${encodeURIComponent(s.id)}/tokens`)
  return (
    <div className="flex flex-col gap-3.5">
      <KpiStrip s={s} now={now} />
      <section aria-labelledby="ctx-chart" className="flex flex-col gap-2 rounded-panel border border-border p-3">
        <h3 id="ctx-chart" className={caption}>{M.contextOverTime}</h3>
        <Panel read={series}>
          {(d) => (d.points ?? []).length < 2 ? <p className="text-xs text-fg-muted">{M.noSeries}</p> : (
            <>
              <ContextChart d={d} />
              <div className="flex flex-wrap gap-x-3.5 gap-y-1 font-mono text-caption text-fg-muted">
                {(d.compactions ?? []).map((c) => <span key={c.at}>⟲ {M.compaction}{c.before != null && c.after != null ? ` ${compact(c.before)} → ${compact(c.after)}` : ''} ({ageText(now - c.at)} ago){c.inferred ? ` · ${M.inferred}` : ''}</span>)}
                <span>- - {M.warnLine(d.warn_pct)}</span>
                {(d.clears ?? []).length > 0 && <span>{M.lastClearLegend} {ageText(now - d.clears[d.clears.length - 1].at)} ago{d.clears[d.clears.length - 1].inferred ? ` · ${M.inferred}` : ''}</span>}
              </div>
            </>
          )}
        </Panel>
      </section>
      <section aria-labelledby="ctx-tokens" className="flex flex-col gap-2 rounded-panel border border-border p-3">
        <h3 id="ctx-tokens" className={caption}>{M.tokensTitle}</h3>
        <Panel read={tokens}>
          {(t) => !t.metered ? <p title={M.unmeteredReason} className="text-xs text-fg-muted">{M.unmetered}</p> : (
            <>
              {/* KIT-GAP: golden also draws a per-kind share and trend sparkline; the API reports one share and trend for the whole session, so they are shown below, not per row. */}
              <div className="grid grid-cols-[1.4fr_1fr_1fr] gap-x-2 text-xs text-fg-secondary">
                {[M.kind, M.thisHour, M.thisSession].map((h) => <div key={h} className={cn('border-b border-border-subtle pb-1', caption)}>{h}</div>)}
                {([['input', M.input], ['output', M.output], ['cache_read', M.cacheRead], ['cache_write', M.cacheWrite]] as const).map(([k, label]) => (
                  <div key={k} className="contents"><div className="border-b border-border-subtle py-1.5">{label}</div><div className="border-b border-border-subtle py-1.5 font-mono tabular-nums">{compact(t.hour?.[k])}</div><div className="border-b border-border-subtle py-1.5 font-mono tabular-nums">{compact(t.session?.[k])}</div></div>
                ))}
              </div>
              <p className="text-xs text-fg-muted">{M.share}: {t.share === null ? M.na : `${Math.round(t.share * 100)}%`}{t.trend ? ` · ${M.trend[t.trend]}` : ''}</p>
            </>
          )}
        </Panel>
      </section>
    </div>
  )
}

// ---------- History ----------

interface History { sessions: { session_id: string; started_at: number; ran_ms: number; tokens: Record<string, number>; tool_calls: number; ended_at: number; end_reason: string }[] }

const when = (at: number) => new Date(at).toLocaleString([], { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit', hour12: false })
const failed = (reason: string) => /fail|error/i.test(reason)

export function HistoryTab({ s }: { s: Session }) {
  const read = useAgentsRead<History>(`/sessions/${encodeURIComponent(s.id)}/history`)
  const total = s.tokens.metered ? compact((s.tokens.input ?? 0) + (s.tokens.output ?? 0)) : M.unmetered
  return (
    <section aria-labelledby="hist" className="flex flex-col gap-3.5">
      <h3 id="hist" className={caption}>{M.historyTitle}</h3>
      <Panel read={read}>
        {(d) => (d.sessions ?? []).length === 0 ? <p className="text-xs text-fg-muted">{M.historyEmpty}</p> : (
          <div className="grid grid-cols-[1.2fr_80px_90px_90px_1.4fr] gap-x-2 text-xs text-fg-secondary max-sm:grid-cols-[1fr_70px_70px]">
            {[M.started, M.ran, M.tokensTitle, M.toolCalls, M.ended].map((h, i) => <div key={h} className={cn('border-b border-border-subtle pb-1', caption, i > 2 && 'max-sm:hidden')}>{h}</div>)}
            {[{ id: s.id, started_at: s.started_at, ran_ms: s.runtime_ms, tokens: total, tools: s.tool_call_count, reason: M.running, current: true }, ...(d.sessions ?? []).map((h) => ({ id: h.session_id, started_at: h.started_at, ran_ms: h.ran_ms, tokens: h.tokens ? compact((h.tokens.input ?? 0) + (h.tokens.output ?? 0)) : M.unmetered, tools: h.tool_calls, reason: h.end_reason, current: false }))].map((h) => (
              <div key={h.id} className="contents">
                <div className="border-b border-border-subtle py-1.5 text-fg"><Link to={`/dashboards/transcripts?session=${encodeURIComponent(h.id)}`} className="underline-offset-2 hover:underline">{when(h.started_at)}</Link>{h.current && ` · ${M.current}`}</div>
                <div className="border-b border-border-subtle py-1.5 font-mono">{h.ran_ms > 0 ? ageText(h.ran_ms) : M.na}</div>
                <div className="border-b border-border-subtle py-1.5 font-mono">{h.tokens}</div>
                <div className="border-b border-border-subtle py-1.5 font-mono max-sm:hidden">{h.tools === null ? M.na : fmt(h.tools)}</div>
                <div className="border-b border-border-subtle py-1.5 max-sm:hidden">{failed(h.reason) ? <Chip tone="danger">{h.reason}</Chip> : <span className="text-fg-muted">{h.reason}</span>}</div>
              </div>
            ))}
          </div>
        )}
      </Panel>
    </section>
  )
}

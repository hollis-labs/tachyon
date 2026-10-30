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

export function NowTab({ s, now }: { s: Session; now: number }) {
  const latest = useAgentsRead<EventsResponse>(`/sessions/${encodeURIComponent(s.id)}/events?group_runs=1&limit=8`)
  const a = s.assignment
  const hasAssignment = !!a && !!(a.task_ref || a.theme_ref)
  const na = { value: M.na, title: AGENTS.naReason }
  const tokens = s.tokens.metered ? { value: fmt((s.tokens.input ?? 0) + (s.tokens.output ?? 0)), title: undefined } : { value: M.unmetered, title: M.unmeteredReason }
  const clear = s.last_clear_at ? { value: `${s.last_clear_at.inferred ? '~' : ''}${ageText(now - s.last_clear_at.at)} ago`, title: s.last_clear_at.inferred ? M.inferred : undefined } : { value: M.never, title: undefined }
  const kpis: [string, ReactNode, string | undefined][] = [
    [M.kpi.state, AGENTS.states[s.state], undefined],
    [M.kpi.runtime, s.runtime_ms > 0 ? ageText(s.runtime_ms) : na.value, s.runtime_ms > 0 ? undefined : na.title],
    [M.kpi.context, s.context.pct === null ? na.value : `${s.context.pct}%`, s.context.pct === null ? AGENTS.contextNa : undefined],
    [M.kpi.tokens, tokens.value, tokens.title],
    [M.kpi.calls, s.tool_call_count === null ? na.value : fmt(s.tool_call_count), s.tool_call_count === null ? na.title : undefined],
    [M.kpi.lastClear, clear.value, clear.title],
  ]
  const st = a?.stats
  return (
    <div className="flex flex-col gap-4">
      <div className="grid grid-cols-2 gap-px overflow-hidden rounded-control border border-border-subtle bg-border-subtle sm:grid-cols-3 lg:grid-cols-6">
        {kpis.map(([k, v, t]) => <Kpi key={k} label={k} value={v} title={t} />)}
      </div>
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
        <Panel read={latest}>{(d) => <EventFeed entries={toTimeline(d, now)} now={now} label={M.latest} />}</Panel>
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

interface Series { points: { at: number; pct: number }[]; compactions: { at: number; inferred: boolean }[]; clears: { at: number; inferred: boolean }[]; warn_pct: number }
interface Tokens { metered: boolean; hour: Record<string, number> | null; session: Record<string, number> | null; share: number | null; trend: 'up' | 'down' | 'flat' | null }

function ContextChart({ d }: { d: Series }) {
  const w = 600
  const h = 140
  const t0 = d.points[0].at
  const span = Math.max(1, d.points[d.points.length - 1].at - t0)
  const x = (at: number) => ((at - t0) / span) * w
  const y = (pct: number) => h - (Math.min(100, pct) / 100) * h
  const path = d.points.map((p, i) => `${i ? 'L' : 'M'}${x(p.at).toFixed(1)} ${y(p.pct).toFixed(1)}`).join(' ')
  return (
    <svg viewBox={`0 0 ${w} ${h}`} role="img" aria-label={M.contextOverTime} className="h-40 w-full text-fg" fill="none" stroke="currentColor" strokeWidth="1.5">
      <line x1="0" x2={w} y1={y(d.warn_pct)} y2={y(d.warn_pct)} strokeDasharray="4 4" className="text-danger" />
      <path d={path} />
      {d.compactions.map((c) => <line key={c.at} x1={x(c.at)} x2={x(c.at)} y1="0" y2={h} strokeDasharray={c.inferred ? '2 3' : undefined} className="text-fg-faint" strokeWidth="1" />)}
    </svg>
  )
}

export function ContextTab({ s }: { s: Session }) {
  const series = useAgentsRead<Series>(`/sessions/${encodeURIComponent(s.id)}/context-series`)
  const tokens = useAgentsRead<Tokens>(`/sessions/${encodeURIComponent(s.id)}/tokens`)
  return (
    <div className="flex flex-col gap-6">
      <section className="flex flex-col gap-2">
        <h3 className={heading}>{M.contextOverTime}</h3>
        <Panel read={series}>
          {(d) => d.points.length < 2 ? <p className="dash-support text-fg-muted">{M.noSeries}</p> : (
            <>
              <ContextChart d={d} />
              <p className="dash-caption text-fg-faint">{M.warnLine(d.warn_pct)} · {d.compactions.length} compactions · {d.clears.length} clears</p>
            </>
          )}
        </Panel>
      </section>
      <section className="flex flex-col gap-2">
        <h3 className={heading}>{M.tokensTitle}</h3>
        <Panel read={tokens}>
          {(t) => !t.metered ? <p className="dash-support text-fg-muted">{M.unmetered}</p> : (
            <>
              <table className="w-full dash-support">
                <thead><tr className="text-left text-fg-faint"><th className="py-1 font-normal"> </th><th className="font-normal">{M.thisHour}</th><th className="font-normal">{M.thisSession}</th></tr></thead>
                <tbody>
                  {([['input', M.input], ['output', M.output], ['cache_read', M.cacheRead], ['cache_write', M.cacheWrite]] as const).map(([k, label]) => (
                    <tr key={k} className="border-t border-border-subtle"><td className="py-1 text-fg-muted">{label}</td><td className="tabular-nums text-fg">{fmt(t.hour?.[k])}</td><td className="tabular-nums text-fg">{fmt(t.session?.[k])}</td></tr>
                  ))}
                </tbody>
              </table>
              <p className="dash-caption text-fg-faint">{M.share}: {t.share === null ? M.unknown : `${Math.round(t.share * 100)}%`}{t.trend ? ` · ${M.trend[t.trend]}` : ''}</p>
            </>
          )}
        </Panel>
      </section>
    </div>
  )
}

// ---------- History ----------

interface History { sessions: { session_id: string; started_at: number; ran_ms: number; tokens: Record<string, number>; tool_calls: number; ended_at: number; end_reason: string }[] }

export function HistoryTab({ s }: { s: Session }) {
  const read = useAgentsRead<History>(`/sessions/${encodeURIComponent(s.id)}/history`)
  return (
    <Panel read={read}>
      {(d) => d.sessions.length === 0 ? <p className="dash-support text-fg-muted">{M.historyEmpty}</p> : (
        <table className="w-full dash-support">
          <thead><tr className="text-left text-fg-faint"><th className="font-normal">{M.started}</th><th className="font-normal">{M.ran}</th><th className="font-normal">{M.tokensTitle}</th><th className="font-normal">{M.toolCalls}</th><th className="font-normal">{M.endReason}</th></tr></thead>
          <tbody>
            {d.sessions.map((h) => (
              <tr key={h.session_id} className="border-t border-border-subtle text-fg">
                <td className="py-1">{new Date(h.started_at).toLocaleString()}</td><td>{ageText(h.ran_ms)}</td>
                <td className="tabular-nums">{fmt((h.tokens.input ?? 0) + (h.tokens.output ?? 0))}</td><td className="tabular-nums">{fmt(h.tool_calls)}</td><td>{h.end_reason}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </Panel>
  )
}

import type { ReactNode } from 'react'
import { Skeleton } from '@hollis-labs/design-components'
import { EventTimeline, SegmentedProgress, type TimelineEvent } from '@planner/shared'
import { useAgentsRead } from './api'
import { ContextMeter, OneLiner, ageText } from './session-parts'
import { AGENTS } from './vocabulary'
import type { Session } from './types'

const M = AGENTS.modal
const heading = 'font-mono dash-caption uppercase tracking-label text-fg-faint'

const fmt = (n: number | null | undefined) => (n === null || n === undefined ? M.unknown : n.toLocaleString())
const dayLabel = (at: number, now: number) => {
  const days = Math.floor(now / 86_400_000) - Math.floor(at / 86_400_000)
  return days === 0 ? 'Today' : days === 1 ? 'Yesterday' : new Date(at).toLocaleDateString()
}
const clock = (at: number) => new Date(at).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })

function Panel<T>({ read, children }: { read: { data: T | null; error: string | null; reload: () => void }; children: (d: T) => ReactNode }) {
  if (read.error) return <p role="alert" className="dash-support text-danger">{M.loadFailed}: {read.error} <button type="button" className="underline" onClick={() => void read.reload()}>{M.retry}</button></p>
  if (read.data === null) return <div aria-busy className="flex flex-col gap-2">{[0, 1, 2].map((i) => <Skeleton key={i} className="h-8 w-full" />)}</div>
  return <>{children(read.data)}</>
}

function Row({ label, value }: { label: string; value: ReactNode }) {
  return <div className="flex justify-between gap-4 border-b border-border-subtle py-1.5 dash-support"><dt className="text-fg-muted">{label}</dt><dd className="text-fg">{value}</dd></div>
}

// ---------- Now ----------

interface EventsResponse { events: ApiEvent[]; gaps: Gap[]; next_before: number | null }
interface ApiEvent { id?: string; type: string; at?: number; started_at?: number; summary: string; actor?: string; calls?: number; events?: ApiEvent[]; run_id?: string; card?: { title: string; summary: string } | null }
interface Gap { start: number; end: number; reason: string; backfilled: boolean }

export function NowTab({ s, now }: { s: Session; now: number }) {
  const latest = useAgentsRead<EventsResponse>(`/sessions/${encodeURIComponent(s.id)}/events?limit=5`)
  const a = s.assignment
  return (
    <div className="flex flex-col gap-5">
      <dl className="grid grid-cols-2 gap-x-6 sm:grid-cols-3 xl:grid-cols-6">
        {[
          [M.kpi.state, AGENTS.states[s.state]],
          [M.kpi.runtime, ageText(s.runtime_ms)],
          [M.kpi.context, s.context.pct === null ? M.unknown : `${s.context.pct}%`],
          [M.kpi.tokens, s.tokens.metered ? fmt((s.tokens.input ?? 0) + (s.tokens.output ?? 0)) : M.unmetered],
          [M.kpi.calls, fmt(s.tool_call_count)],
          [M.kpi.lastClear, s.last_clear_at ? ageText(now - s.last_clear_at.at) : M.never],
        ].map(([k, v]) => (
          <div key={k} className="flex flex-col"><dt className="dash-caption text-fg-faint">{k}</dt><dd className="dash-support text-fg">{v}</dd></div>
        ))}
      </dl>
      <section aria-labelledby="now-right" className="flex flex-col gap-1">
        <h3 id="now-right" className={heading}>{M.rightNow}</h3>
        {s.one_liner ? (
          <>
            <OneLiner line={s.one_liner} now={now} fallback="" />
            <p className="dash-caption text-fg-faint">{s.one_liner.tool} · {M.since} {ageText(now - s.one_liner.at)} · {M.callsThisTurn(s.one_liner.calls_this_turn)}</p>
          </>
        ) : <p className="dash-support text-fg-muted">{M.nothingNow}</p>}
      </section>
      <section aria-labelledby="now-assigned" className="flex flex-col gap-1">
        <h3 id="now-assigned" className={heading}>{M.assigned}</h3>
        {a ? (
          <>
            <p className="dash-support text-fg">{a.theme_ref} <span className="text-fg-muted">{a.task_ref}</span></p>
            {a.index !== undefined && a.count !== undefined && <p className="dash-caption text-fg-muted">{M.taskOf(a.index, a.count)}</p>}
            {a.count ? <SegmentedProgress label={M.taskOf(a.index ?? 0, a.count)} items={[{ state: 'done', count: Math.max(0, (a.index ?? 1) - 1) }, { state: 'doing', count: 1 }, { state: 'todo', count: Math.max(0, a.count - (a.index ?? 1)) }]} /> : null}
          </>
        ) : <p className="dash-support text-fg-muted">{M.noAssignment}</p>}
      </section>
      <section aria-labelledby="now-context" className="flex flex-col gap-1">
        <h3 id="now-context" className={heading}>{M.contextTitle}</h3>
        <ContextMeter pct={s.context.pct} />
        <p className="dash-caption text-fg-faint">{s.context.used !== null && s.context.window !== null ? `${fmt(s.context.used)} / ${fmt(s.context.window)}. ` : ''}{M.contextTotalOnly}</p>
      </section>
      <section aria-labelledby="now-health" className="flex flex-col">
        <h3 id="now-health" className={heading}>{M.health}</h3>
        <dl>
          <Row label={M.provider} value={`${s.provider}${s.model ? ` · ${s.model}` : ''}`} />
          <Row label={M.lastActivity} value={s.last_activity_at ? `${ageText(now - s.last_activity_at)}` : M.unknown} />
          <Row label={M.pendingApproval} value={s.pending.known ? (s.pending.approval ? M.yes : M.no) : M.unknown} />
          <Row label={M.pendingQuestion} value={s.pending.known ? (s.pending.question ? M.yes : M.no) : M.unknown} />
        </dl>
      </section>
      <section aria-labelledby="now-latest" className="flex flex-col gap-1">
        <h3 id="now-latest" className={heading}>{M.latest}</h3>
        <Panel read={latest}>{(d) => <ul className="flex flex-col gap-1">{d.events.map((e, i) => <li key={e.id ?? e.run_id ?? i} className="dash-support text-fg"><span className="mr-2 font-mono dash-caption text-fg-faint">{clock(e.at ?? e.started_at ?? 0)}</span>{e.summary}</li>)}</ul>}</Panel>
      </section>
    </div>
  )
}

// ---------- Timeline ----------

const FACET_OF: Record<string, string> = { tool: 'tools', message: 'messages', approval: 'approvals', error: 'errors', lifecycle: 'lifecycle', compaction: 'lifecycle', clear: 'lifecycle', task: 'lifecycle', control: 'lifecycle' }

export function toTimeline(d: EventsResponse, now: number): TimelineEvent[] {
  const rows: TimelineEvent[] = d.events.map((e, i) => {
    const at = e.at ?? e.started_at ?? 0
    const isRun = e.type === 'tool_run'
    return {
      id: e.id ?? e.run_id ?? `e${i}`,
      day: dayLabel(at, now),
      time: clock(at),
      facet: isRun ? 'tools' : FACET_OF[e.type] ?? 'lifecycle',
      tone: e.type === 'error' ? 'blocked' : 'neutral',
      body: isRun ? (
        <details>
          <summary className="cursor-pointer">{M.run(e.calls ?? 0)}: {e.summary}</summary>
          <ul className="mt-1 flex flex-col gap-0.5 pl-3">{(e.events ?? []).map((c) => <li key={c.id} className="dash-caption text-fg-muted">{clock(c.at ?? 0)} {c.summary}</li>)}</ul>
        </details>
      ) : e.summary,
      card: e.card ? <span><strong className="text-fg">{e.card.title}</strong> {e.card.summary}</span> : undefined,
    }
  })
  // Collection gaps are shown where they happened, never filled in.
  const gaps: (TimelineEvent & { at: number })[] = d.gaps.map((g, i) => ({ at: g.end, id: `gap${i}`, day: dayLabel(g.end, now), time: clock(g.end), facet: 'lifecycle', body: <em>{M.gap(g.reason)}</em> }))
  const stamp = new Map<string, number>(d.events.map((e, i) => [e.id ?? e.run_id ?? `e${i}`, e.at ?? e.started_at ?? 0]))
  return [...rows.map((r) => ({ ...r, at: stamp.get(r.id) ?? 0 })), ...gaps].sort((a, b) => b.at - a.at).map(({ at: _at, ...r }) => r)
}

export function TimelineTab({ s, now }: { s: Session; now: number }) {
  const read = useAgentsRead<EventsResponse>(`/sessions/${encodeURIComponent(s.id)}/events?group_runs=1&limit=100`)
  return (
    <Panel read={read}>
      {(d) => {
        const events = toTimeline(d, now)
        const facets = (Object.keys(M.facet) as (keyof typeof M.facet)[]).map((f) => ({ id: f, label: M.facet[f], count: f === 'all' ? events.length : events.filter((e) => e.facet === f).length }))
        return <EventTimeline events={events} facets={facets} allFacet="all" facetsLabel={M.timelineFilters} streamLabel={M.timelineStream} emptyLabel={M.timelineEmpty} filteredEmptyLabel={M.timelineNoMatch} />
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

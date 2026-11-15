import { useCallback, useEffect, useState } from 'react'
import { AlertTriangle } from 'lucide-react'
import { Skeleton } from '@hollis-labs/design-components'
import { Chip } from '@planner/shared'
import { RefChip } from '../autolink'
import { consoleJson, type FetchLike } from '../lib/http'
import { ageText } from './session-parts'
import { CEREMONY } from './vocabulary'

/** GET ceremony (CW-20261114-0023f): the ceremony meter over the last 7 days. */
export interface CeremonyMeter {
  checked_at: string
  window_days: number
  gates: { total: number; reviews: number; approvals: number; checkpoints: number; asks: number }
  median_wait_seconds: number | null
  wait_samples: number
  stalled: { id: string; title: string; status: string; updated_at: string }[]
  stalled_total: number
  pending_checkpoints: { id: string; task_id: string; type: string; title: string; emitted_at: string }[]
  pending_checkpoints_total: number
  activity: number
  done: number
  previous: { day: string; activity: number; done: number; gates: number } | null
  comparison: 'pending' | 'steady' | 'regression'
  sources: Record<string, { ok: boolean; error?: string }>
}

const wait = (s: number) => (s < 3600 ? `${Math.max(1, Math.round(s / 60))}m` : s < 48 * 3600 ? `${(s / 3600).toFixed(1)}h` : `${Math.round(s / 86400)}d`)

function Stat({ label, value, detail, tone }: { label: string; value: string; detail?: string; tone?: 'danger' }) {
  return (
    <div className="flex min-w-36 flex-1 flex-col gap-0.5 rounded-panel border border-border px-3 py-2.5" data-stat={label}>
      <span className="text-label text-fg-muted">{label}</span>
      <span className={`font-mono text-title tabular-nums ${tone === 'danger' ? 'text-danger' : 'text-fg'}`}>{value}</span>
      {detail && <span className="text-label text-fg-muted">{detail}</span>}
    </div>
  )
}

export function CeremonyView({ m, now }: { m: CeremonyMeter; now: number }) {
  const failed = Object.entries(m.sources).filter(([, s]) => !s.ok).map(([k]) => CEREMONY.source[k] ?? k)
  const g = m.gates
  return (
    <div className="flex flex-col gap-4">
      {failed.length > 0 && <p role="alert" className="flex items-center gap-2 text-label text-warning"><AlertTriangle className="size-3.5" />{CEREMONY.incomplete(failed)}</p>}
      <div className="flex flex-wrap gap-3">
        <Stat label={CEREMONY.gates} value={String(g.total)} detail={CEREMONY.gatesDetail(g)} />
        <Stat label={CEREMONY.medianWait} value={m.median_wait_seconds === null ? '-' : wait(m.median_wait_seconds)} detail={CEREMONY.samples(m.wait_samples)} />
        <Stat label={CEREMONY.stalled} value={String(m.stalled_total)} detail={CEREMONY.stalledDetail} />
        <Stat label={CEREMONY.activityVsDone} value={`${m.activity.toLocaleString()} / ${m.done}`} tone={m.comparison === 'regression' ? 'danger' : undefined}
          detail={m.previous ? CEREMONY.versus(m.previous.activity, m.previous.done) : CEREMONY.comparisonPending} />
      </div>
      {m.comparison === 'regression' && <p role="alert" className="text-control text-danger">{CEREMONY.regression}</p>}
      <section aria-label={CEREMONY.pendingTitle} className="flex flex-col gap-2">
        <h3 className="font-mono text-caption uppercase tracking-label text-fg-muted">{CEREMONY.pendingTitle} · {m.pending_checkpoints_total}</h3>
        {m.pending_checkpoints.length === 0 ? <p className="text-xs text-fg-muted">{CEREMONY.nonePending}</p> : (
          <ul className="flex flex-col divide-y divide-border rounded-panel border border-border">
            {m.pending_checkpoints.map((c) => (
              <li key={c.id} data-checkpoint={c.id} className="flex items-center gap-2 px-3 py-2 text-xs">
                <Chip>{CEREMONY.cpType(c.type)}</Chip>
                <RefChip kind="torque.task" canonical={c.task_id} raw={c.task_id} resolve="hover" />
                <span className="min-w-0 flex-1 truncate text-fg">{c.title}</span>
                <span className="shrink-0 text-fg-muted">{ageText(now - Date.parse(c.emitted_at))}</span>
              </li>
            ))}
          </ul>
        )}
      </section>
      <section aria-label={CEREMONY.stalledTitle} className="flex flex-col gap-2">
        <h3 className="font-mono text-caption uppercase tracking-label text-fg-muted">{CEREMONY.stalledTitle} · {m.stalled_total}</h3>
        {m.stalled.length === 0 ? <p className="text-xs text-fg-muted">{CEREMONY.noneStalled}</p> : (
          <ul className="flex flex-col divide-y divide-border rounded-panel border border-border">
            {m.stalled.map((s) => (
              <li key={s.id} data-stalled={s.id} className="flex items-center gap-2 px-3 py-2 text-xs">
                <Chip>{s.status}</Chip>
                <RefChip kind="torque.task" canonical={s.id} raw={s.id} resolve="hover" />
                <span className="min-w-0 flex-1 truncate text-fg">{s.title}</span>
                <span className="shrink-0 text-fg-muted">{CEREMONY.idle(ageText(now - Date.parse(s.updated_at)))}</span>
              </li>
            ))}
          </ul>
        )}
      </section>
    </div>
  )
}

/** The Ceremony tab: how much process the work costs, and whether activity is turning into progress. */
export function CeremonyTab({ now, fetch: f }: { now: number; fetch?: FetchLike }) {
  const [m, setM] = useState<CeremonyMeter | null>(null)
  const [error, setError] = useState<string | null>(null)
  const load = useCallback(async () => {
    try {
      setM(await consoleJson<CeremonyMeter>('/ceremony', { fetch: f }))
      setError(null)
    } catch (e) {
      setError(e instanceof Error ? e.message : CEREMONY.loadFailed)
    }
  }, [f])
  useEffect(() => {
    void load()
    const t = setInterval(() => void load(), 5 * 60_000)
    return () => clearInterval(t)
  }, [load])
  return (
    <div className="min-h-0 flex-1 overflow-auto">
      <div className="flex max-w-[1100px] flex-col gap-3 p-4">
        <p className="text-label text-fg-muted">{CEREMONY.intro}</p>
        {error && <p role="alert" className="text-xs text-danger">{CEREMONY.loadFailed}: {error}</p>}
        {m === null ? (!error && <div aria-busy className="flex gap-3">{[0, 1, 2, 3].map((i) => <Skeleton key={i} className="h-20 flex-1" />)}</div>) : <CeremonyView m={m} now={now} />}
      </div>
    </div>
  )
}

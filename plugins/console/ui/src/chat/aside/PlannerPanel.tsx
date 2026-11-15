import { useCallback, useEffect, useState } from 'react'
import { CAPTION, Chip, SegmentedProgress, StateDot } from '@planner/shared'
import { registerPanel } from './registry'

const BASE = '/api/plugins/console/v1/planner'

interface ThemeRow { id: string; title: string; status: string; next_step?: string; blocked_count: number; pending_decisions?: { id: string; title: string }[]; progress: { available: boolean; done?: number; total?: number; counts?: Record<string, number> } }
interface Board { sections: { in_flight?: ThemeRow[]; cleared?: ThemeRow[]; queued?: ThemeRow[] }; counts: Record<string, number>; progress: { done: number; total: number } }
interface NeedYou { total: number; by_reason: Record<string, number>; items: { db: string; id: string; reasons: string[] }[] }
interface Decision { id: string; title: string; status?: string; tldr?: string }

/** One live read of the Planner API; `reload` after the server pushes or the user asks. */
function usePlanner<T>(path: string) {
  const [state, set] = useState<{ data: T | null; loading: boolean; error: string }>({ data: null, loading: true, error: '' })
  const load = useCallback(async () => {
    try {
      const res = await fetch(`${BASE}${path}`, { cache: 'no-store', headers: { Accept: 'application/json' } })
      if (!res.ok) throw new Error(res.status === 401 || res.status === 403 ? 'The Planner needs operator access.' : `Planner answered ${res.status}.`)
      set({ data: (await res.json()) as T, loading: false, error: '' })
    } catch (e) { set((s) => ({ ...s, loading: false, error: e instanceof Error ? e.message : 'Could not load the Planner.' })) }
  }, [path])
  useEffect(() => { void load() }, [load])
  return { ...state, reload: load }
}

const Err = ({ children }: { children: string }) => (children ? <p role="alert" className="text-xs text-danger">{children}</p> : null)
const Soft = ({ children }: { children: string }) => <p className="text-xs text-fg-muted">{children}</p>
const FULL = <a href="/planner" className="text-label text-fg-secondary underline">Open the full Planner</a>

function ThemeCard({ t }: { t: ThemeRow }) {
  const c = t.progress.counts ?? {}
  const done = t.progress.done ?? 0, total = t.progress.total ?? 0
  return (
    <li className="flex flex-col gap-1 rounded-control border border-border px-2.5 py-2">
      <div className="flex items-center gap-2 text-control font-medium"><span className="min-w-0 flex-1 truncate">{t.title}</span>{t.blocked_count > 0 && <StateDot state="blocked" />}{(t.pending_decisions?.length ?? 0) > 0 && <Chip square>decide {t.pending_decisions?.length}</Chip>}</div>
      {t.progress.available && total > 0 && (
        <SegmentedProgress size="sm" className="h-[3px]" label={`${t.title}: ${done} of ${total} tasks done`} items={[{ state: 'done', count: done }, { state: 'doing', count: c.doing ?? 0 }, { state: 'blocked', count: c.blocked ?? 0 }, { state: 'todo', count: Math.max(0, total - done - (c.doing ?? 0) - (c.blocked ?? 0)) }]} />
      )}
      {t.next_step && <span className="truncate text-xs text-fg-muted">Next: {t.next_step}</span>}
    </li>
  )
}

/** Board: what the portfolio is doing now, read from the Planner theme board. */
export function PlannerBoardTab() {
  const b = usePlanner<Board>('/board?scope=themes&limit=8')
  if (b.loading) return <p role="status" className="text-xs text-fg-muted">Loading the board…</p>
  const rows = [...(b.data?.sections.in_flight ?? []), ...(b.data?.sections.cleared ?? [])]
  return (
    <>
      <div className="flex items-center gap-2"><span className={CAPTION}>Portfolio · in flight</span><span className="flex-1" />{b.data && <span className="font-mono text-caption text-fg-muted">{b.data.progress.done}/{b.data.progress.total}</span>}</div>
      <Err>{b.error}</Err>
      {!b.error && (rows.length === 0 ? <Soft>Nothing is in flight.</Soft> : <ul className="flex flex-col gap-1.5">{rows.map((t) => <ThemeCard key={t.id} t={t} />)}</ul>)}
      <p className="text-xs text-fg-muted">{FULL}</p>
    </>
  )
}

/** Need you: the Planner's need-you facet and the decisions waiting. */
export function PlannerNeedTab() {
  const n = usePlanner<NeedYou>('/need-you')
  const d = usePlanner<Decision[]>('/decisions?status=proposed')
  if (n.loading || d.loading) return <p role="status" className="text-xs text-fg-muted">Loading what needs you…</p>
  const waiting = d.data ?? []
  return (
    <>
      <div className="flex items-center gap-2"><span className={CAPTION}>Need you</span><span className="flex-1" />{n.data && <span className="font-mono text-caption text-fg-muted">{n.data.total}</span>}</div>
      <Err>{n.error || d.error}</Err>
      {n.data && Object.keys(n.data.by_reason).length > 0 && <div className="flex flex-wrap gap-1.5">{Object.entries(n.data.by_reason).map(([r, c]) => <Chip key={r} square>{r.replace(/_/g, ' ')} {c}</Chip>)}</div>}
      {!d.error && (waiting.length === 0 ? <Soft>No decisions are waiting.</Soft> : (
        <section className="flex flex-col gap-1.5"><span className={CAPTION}>Decisions waiting · {waiting.length}</span>
          <ul className="flex flex-col gap-1.5">{waiting.slice(0, 12).map((x) => <li key={x.id} className="flex flex-col gap-0.5 rounded-control border border-border px-2.5 py-2 text-xs"><span className="text-control font-medium text-fg">{x.title}</span>{x.tldr && <span className="line-clamp-2 text-fg-muted">{x.tldr}</span>}</li>)}</ul>
        </section>
      ))}
      <p className="text-xs text-fg-muted">{FULL}</p>
    </>
  )
}

/** The Portfolio Manager's panel. Matched by agent id; the roster id of the planner agent is one of these. */
export const PLANNER_AGENTS = ['portfolio-manager', 'planner', 'pm']
registerPanel({
  id: 'planner',
  label: 'Planner',
  owner: 'system',
  appliesTo: { agents: PLANNER_AGENTS },
  tabs: [{ id: 'planner-board', label: 'Board', render: () => <PlannerBoardTab /> }, { id: 'planner-need', label: 'Need you', render: () => <PlannerNeedTab /> }],
})

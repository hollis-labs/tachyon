import { useCallback, useEffect, useMemo, useRef, useState, type FormEvent } from 'react'
import { toast } from 'sonner'
import { Button, Input } from '@hollis-labs/design-components'
import { usePlannerApi } from '@planner/api/context'
import { PlannerApiError } from '@planner/api/errors'
import { notifyWriteError } from '@planner/api/notifications'
import { LANES, TEXT, type LaneId } from '@planner/lib/vocabulary'
import { LaneBoard, StatusMenu, type Lane } from '@planner/shared'
import { DashboardViewTabs } from '@hollis-labs/kit-dashboard/layout'
import type { PrCite, TaskLane, ThemeTask, ThemeTasks } from '@planner/api/types'

/** True below the sm breakpoint, where lanes become a switcher. */
function useNarrow() {
  const q = '(max-width: 639px)'
  const [narrow, setNarrow] = useState(() => typeof window !== 'undefined' && typeof window.matchMedia === 'function' && window.matchMedia(q).matches)
  useEffect(() => {
    if (typeof window.matchMedia !== 'function') return
    const m = window.matchMedia(q)
    const on = () => setNarrow(m.matches)
    m.addEventListener('change', on)
    return () => m.removeEventListener('change', on)
  }, [])
  return narrow
}

const laneTitle = (id: string) => LANES.find((l) => l.id === id)?.title ?? id
const prHref = (c: PrCite) => `https://${c.host}/${c.owner}/${c.repo}/pull/${c.number}`

/**
 * A theme's Torque tasks by lane. A drop is a real status transition: the card moves at once, the call
 * settles after, Undo for 8 s is the inverse transition, and a 409 snaps the card to the status Torque
 * has now. Blocked asks for a reason first; Done saves at once and the "What shipped?" line is attached
 * afterwards, the move never waits for it.
 */
export function TaskBoard({ themeId, paused, onNeedsAccess, accessNoteId }: { themeId: string; paused?: boolean; onNeedsAccess?: () => void; accessNoteId?: string }) {
  const api = usePlannerApi()
  const [data, setData] = useState<ThemeTasks | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [pending, setPending] = useState<Set<string>>(new Set())
  const [shipped, setShipped] = useState<string | null>(null)
  const [summary, setSummary] = useState('')
  const [blocking, setBlocking] = useState<{ id: string; from: TaskLane } | null>(null)
  const [reason, setReason] = useState('')
  const [activeLane, setActiveLane] = useState<LaneId>('doing')
  const narrow = useNarrow()
  const live = useRef(true)
  useEffect(() => () => { live.current = false }, [])

  const load = useCallback(async () => {
    try {
      const d = await api.plannerThemeTasks(themeId)
      if (live.current) { setData(d); setError(null) }
    } catch (e) {
      if (live.current) setError(e instanceof PlannerApiError && [404, 405].includes(e.status) ? TEXT.tasksUnavailable : e instanceof Error ? e.message : TEXT.tasksFailed)
    }
  }, [api, themeId])
  useEffect(() => { setData(null); void load() }, [load])

  const tasks = useMemo(() => (data ? LANES.flatMap((l) => data.lanes[l.id] ?? []) : []), [data])
  const byId = useMemo(() => new Map(tasks.map((t) => [t.id, t])), [tasks])

  const setStatus = (id: string, status: TaskLane) =>
    setData((d) => {
      if (!d) return d
      const moved = LANES.flatMap((l) => d.lanes[l.id] ?? []).find((t) => t.id === id)
      if (!moved) return d
      const lanes = Object.fromEntries(LANES.map((l) => [l.id, (d.lanes[l.id] ?? []).filter((t) => t.id !== id)])) as ThemeTasks['lanes']
      lanes[status] = [{ ...moved, status }, ...lanes[status]]
      return { ...d, lanes }
    })

  async function transition(id: string, from: TaskLane, to: TaskLane, why?: string, undoable = true) {
    const task = byId.get(id)
    setStatus(id, to)
    setPending((p) => new Set(p).add(id))
    try {
      await api.plannerTaskTransition(id, to, from, why ? { reason: why } : undefined)
      if (undoable)
        toast(TEXT.moved(task?.title ?? id, laneTitle(to)), {
          duration: 8000,
          action: { label: TEXT.undo, onClick: () => void transition(id, to, from, undefined, false) },
        })
      if (to === 'done' && undoable) { setShipped(id); setSummary('') }
    } catch (e) {
      const current = e instanceof PlannerApiError && e.code === 'conflict' ? (e.details?.current as { status?: TaskLane } | undefined)?.status : undefined
      if (current) {
        setStatus(id, current)
        toast(TEXT.changedElsewhere(task?.title ?? id, laneTitle(current)))
      } else {
        setStatus(id, from)
        notifyWriteError(e, TEXT.writeFailed, onNeedsAccess)
      }
    } finally {
      setPending((p) => { const n = new Set(p); n.delete(id); return n })
    }
  }

  const move = (id: string, from: string, to: string) => {
    if (paused) return
    if (to === 'blocked') { setBlocking({ id, from: from as TaskLane }); setReason(''); return }
    void transition(id, from as TaskLane, to as TaskLane)
  }
  const submitBlock = (e: FormEvent) => {
    e.preventDefault()
    if (!blocking || !reason.trim()) return
    const b = blocking
    setBlocking(null)
    void transition(b.id, b.from, 'blocked', reason.trim(), true)
  }
  const endShipped = async (add: boolean) => {
    const id = shipped
    setShipped(null)
    if (!add || !id || !summary.trim()) return
    try {
      await api.plannerTaskMeta(id, { done_summary: summary.trim() })
      setData((d) => d && { ...d, lanes: { ...d.lanes, done: d.lanes.done.map((t) => (t.id === id ? { ...t, done_summary: summary.trim() } : t)) } })
    } catch (e) {
      notifyWriteError(e, TEXT.writeFailed, onNeedsAccess)
    }
  }

  if (error && !data) {
    return (
      <p role="alert" className="flex items-center gap-2 dash-support text-fg-muted">
        {error === TEXT.tasksUnavailable ? error : TEXT.tasksFailed}
        <Button size="sm" variant="outline" onClick={() => void load()}>{TEXT.tasksRetry}</Button>
      </p>
    )
  }
  if (!data) return <p role="status" className="dash-support text-fg-faint">{TEXT.tasksLoading}</p>
  if (!data.counts.total) return <p className="dash-support text-fg-faint">{TEXT.tasksEmpty}</p>

  const card = (t: ThemeTask) => ({
    id: t.id,
    pending: pending.has(t.id),
    content: (
      <span className="flex flex-col gap-0.5">
        <span className="flex items-start gap-2">
          <span className="min-w-0 flex-1 dash-support text-fg">{t.title}</span>
          {/* Drag is never the only way: every card has a Move to menu. */}
          <StatusMenu
            trigger={<span className="dash-caption text-fg-muted hover:text-fg max-sm:inline-flex max-sm:min-h-10 max-sm:items-center max-sm:px-2">{TEXT.moveTo}</span>}
            triggerLabel={TEXT.cardMoveLabel(t.title, laneTitle(t.status))}
            heading={TEXT.moveTo}
            current={t.status}
            options={LANES.map((l) => ({ value: l.id, label: l.title }))}
            onSelect={(to) => move(t.id, t.status, to)}
            disabled={paused || pending.has(t.id)}
            disabledReasonId={accessNoteId}
          />
        </span>
        {(t.pr_cites.length > 0 || t.assignees.length > 0) && (
          <span className="flex flex-wrap items-center gap-x-2 font-mono dash-caption text-fg-muted">
            {t.pr_cites.map((c) => (
              <a key={c.number} href={prHref(c)} target="_blank" rel="noopener noreferrer" className="underline decoration-fg-faint hover:decoration-fg-secondary">{TEXT.prChip(c.number)}</a>
            ))}
            {t.assignees.map((a) => <span key={a.assignment_id}>{a.label ?? a.agent_id ?? a.team_id}</span>)}
          </span>
        )}
        {t.status === 'done' && t.done_summary && <span className="dash-caption text-fg-muted">{t.done_summary}</span>}
        {t.status === 'blocked' && t.blocked_reason && <span className="dash-caption text-fg-muted">{t.blocked_reason}</span>}
      </span>
    ),
  })
  const all: Lane[] = LANES.map((l) => ({ id: l.id, title: l.title, collapsed: l.id === 'done', cards: (data.lanes[l.id as LaneId] ?? []).map(card) }))
  const lanes = narrow ? all.filter((l) => l.id === activeLane) : all
  return (
    <>
      {narrow && (
        <DashboardViewTabs variant="segments" label={TEXT.laneSwitcher} value={activeLane} onChange={(v) => setActiveLane(v as LaneId)} items={LANES.map((l) => ({ value: l.id, label: `${l.title} ${data.lanes[l.id]?.length ?? 0}` }))} />
      )}
      {blocking && (
        <form onSubmit={submitBlock} onKeyDown={(e) => { if (e.key === 'Escape') { e.stopPropagation(); setBlocking(null) } }} className="mb-3 flex flex-wrap items-center gap-2 rounded-md border border-border p-2">
          <label htmlFor="block-reason" className="dash-caption text-fg-muted">{TEXT.blockedReasonLabel}</label>
          <Input id="block-reason" autoFocus value={reason} onChange={(e) => setReason(e.target.value)} className="min-w-0 flex-1" />
          <Button type="submit" size="sm" disabled={!reason.trim()}>{TEXT.blockedReasonAdd}</Button>
          <Button type="button" size="sm" variant="ghost" onClick={() => setBlocking(null)}>{TEXT.blockedReasonCancel}</Button>
          <span className="w-full dash-caption text-fg-faint">{TEXT.blockedReasonHint}</span>
        </form>
      )}
      {shipped && (
        <form onSubmit={(e) => { e.preventDefault(); void endShipped(true) }} onKeyDown={(e) => { if (e.key === 'Escape') { e.stopPropagation(); void endShipped(false) } }} className="mb-3 flex flex-wrap items-center gap-2 rounded-md border border-border p-2">
          <label htmlFor="shipped" className="dash-caption text-fg-muted">{TEXT.doneSummaryLabel}</label>
          <Input id="shipped" autoFocus value={summary} onChange={(e) => setSummary(e.target.value)} className="min-w-0 flex-1" />
          <Button type="submit" size="sm">{TEXT.doneSummaryAdd}</Button>
          <Button type="button" size="sm" variant="ghost" onClick={() => void endShipped(false)}>{TEXT.doneSummarySkip}</Button>
          <span className="w-full dash-caption text-fg-faint">{TEXT.doneSummaryHint}</span>
        </form>
      )}
      <LaneBoard
        lanes={lanes}
        cardTitle={(id) => byId.get(id)?.title ?? id}
        onMove={move}
        readOnly={paused}
        labels={{ lane: TEXT.lane, empty: TEXT.laneEmpty, pickedUp: TEXT.pickedUp, movedOver: TEXT.movedOver, dropped: TEXT.dropped, cancelled: TEXT.cancelled, pending: TEXT.saving, dragHint: TEXT.dragHint }}
      />
    </>
  )
}

import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { toast } from 'sonner'
import { Button } from '@hollis-labs/design-components'
import { usePlannerApi } from '@planner/api/context'
import { PlannerApiError } from '@planner/api/errors'
import { authorizing, notifyWriteError } from '@planner/api/notifications'
import { shortTaskId } from '@planner/lib/planner'
import { LANES, TEXT, type LaneId } from '@planner/lib/vocabulary'
import { FloatingPrompt, LaneBoard, StatusMenu, type Lane } from '@planner/shared'
import type { PrCite, TaskLane, ThemeTask, ThemeTasks } from '@planner/api/types'

const DOT_CLASS: Record<string, string> = { todo: 'bg-fg-faint', doing: 'bg-fg/62', review: 'bg-fg/40', blocked: 'bg-danger', done: 'bg-fg/88' }
const laneTitle = (id: string) => LANES.find((l) => l.id === id)?.title ?? id
const prHref = (c: PrCite) => `https://${c.host}/${c.owner}/${c.repo}/pull/${c.number}`

/**
 * A theme's Torque tasks by lane. A drop is a real status transition: the card moves at once, the call
 * settles after, Undo for 8 s is the inverse transition, and a 409 snaps the card to the status Torque
 * has now. Blocked asks for a reason first; Done saves at once and the "What shipped?" line is attached
 * afterwards, the move never waits for it.
 */
export function TaskBoard({ themeId, tick = 0, paused, onNeedsAccess, accessNoteId }: { themeId: string; tick?: number; paused?: boolean; onNeedsAccess?: () => void; accessNoteId?: string }) {
  const api = usePlannerApi()
  const [data, setData] = useState<ThemeTasks | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [pending, setPending] = useState<Set<string>>(new Set())
  const [shipped, setShipped] = useState<string | null>(null)
  const [summary, setSummary] = useState('')
  const [blocking, setBlocking] = useState<{ id: string; from: TaskLane } | null>(null)
  const [reason, setReason] = useState('')
  const live = useRef(true)
  useEffect(() => { live.current = true; return () => { live.current = false } }, [])

  const load = useCallback(async () => {
    try {
      const d = await api.plannerThemeTasks(themeId)
      if (live.current) { setData(d); setError(null) }
    } catch (e) {
      if (live.current) setError(e instanceof PlannerApiError && [404, 405].includes(e.status) ? TEXT.tasksUnavailable : e instanceof Error ? e.message : TEXT.tasksFailed)
    }
  }, [api, themeId])
  useEffect(() => { setData(null); void load() }, [load])
  // A stream signal refetches in place, but never over a card that is mid-move.
  const settled = useRef(true)
  settled.current = pending.size === 0
  const firstTick = useRef(true)
  useEffect(() => { if (firstTick.current) { firstTick.current = false; return } if (settled.current) void load() }, [tick])

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
      await authorizing(() => api.plannerTaskTransition(id, to, from, why ? { reason: why } : undefined))
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
  const submitBlock = () => {
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
      await authorizing(() => api.plannerTaskMeta(id, { done_summary: summary.trim() }))
      setData((d) => d && { ...d, lanes: { ...d.lanes, done: d.lanes.done.map((t) => (t.id === id ? { ...t, done_summary: summary.trim() } : t)) } })
    } catch (e) {
      notifyWriteError(e, TEXT.writeFailed, onNeedsAccess)
    }
  }

  if (error && !data) {
    return (
      <p role="alert" className="flex items-center gap-2 text-xs text-fg-muted">
        {error === TEXT.tasksUnavailable ? error : TEXT.tasksFailed}
        <Button size="sm" variant="outline" onClick={() => void load()}>{TEXT.tasksRetry}</Button>
      </p>
    )
  }
  if (!data) return <p role="status" className="text-xs text-fg-faint">{TEXT.tasksLoading}</p>
  if (!data.counts.total) return <p className="text-xs text-fg-faint">{TEXT.tasksEmpty}</p>

  const card = (t: ThemeTask) => ({
    id: t.id,
    pending: pending.has(t.id),
    content: (
      <>
        <span className="flex items-center gap-1 font-mono text-caption text-fg-muted">
          <span className="min-w-0 flex-1 truncate" title={t.id}>{shortTaskId(t.id)}</span>
          {t.pr_cites.map((c) => (
            <a key={c.number} href={prHref(c)} target="_blank" rel="noopener noreferrer" onPointerDown={(e) => e.stopPropagation()} className="rounded-sm border border-border px-1 text-fg-secondary hover:text-fg">{TEXT.prChip(c.number)}</a>
          ))}
          {/* Drag is never the only way: every card has a Move to menu (long press on touch). */}
          <StatusMenu
            trigger={<span aria-hidden className="inline-flex px-1 text-fg-muted hover:text-fg max-sm:min-h-10 max-sm:items-center">⋯</span>}
            triggerLabel={TEXT.cardMoveLabel(t.title, laneTitle(t.status))}
            heading={TEXT.moveTo}
            current={t.status}
            options={LANES.map((l) => ({ value: l.id, label: l.title, dotClass: DOT_CLASS[l.id] }))}
            onSelect={(to) => move(t.id, t.status, to)}
            disabled={paused || pending.has(t.id)}
            disabledReasonId={accessNoteId}
            className="opacity-0 focus-visible:opacity-100 group-hover/card:opacity-100 group-focus-within/card:opacity-100 max-sm:opacity-100"
          />
        </span>
        <span className="text-fg">{t.title}</span>
        {t.assignees.length > 0 && <span className="text-fg-muted">{t.assignees.map((a) => a.label ?? a.agent_id ?? a.team_id).join(', ')}</span>}
        {t.status === 'done' && t.done_summary && <span className="text-fg-muted">{t.done_summary}</span>}
        {t.status === 'blocked' && t.blocked_reason && <span className="text-fg-muted">{t.blocked_reason}</span>}
      </>
    ),
  })
  const EMPTY: Partial<Record<LaneId, string>> = { review: TEXT.laneEmptyReview, blocked: TEXT.laneEmptyBlocked }
  const all: Lane[] = LANES.map((l) => ({
    id: l.id,
    title: l.title,
    state: l.id,
    collapsed: l.id === 'done',
    emptyText: EMPTY[l.id],
    add: l.id === 'todo' || l.id === 'doing' ? { label: TEXT.addTask, key: l.id === 'todo' ? TEXT.addTaskKey : undefined, disabled: true, reason: TEXT.addTaskNeedsBackend } : undefined,
    cards: (data.lanes[l.id as LaneId] ?? []).map(card),
  }))
  const movedTask = shipped ? byId.get(shipped) : undefined
  return (
    <>
      {blocking && (
        <FloatingPrompt heading={TEXT.blockedHeading} subject={<PromptSubject task={byId.get(blocking.id)} />} label={TEXT.blockedReasonLabel} value={reason} onChange={setReason} note={TEXT.blockedReasonHint} submitLabel={TEXT.blockedReasonAdd} skipLabel={TEXT.blockedReasonCancel} onSubmit={() => submitBlock()} onSkip={() => setBlocking(null)} />
      )}
      {shipped && (
        <FloatingPrompt heading={TEXT.movedToDone} subject={<PromptSubject task={movedTask} />} label={TEXT.doneSummaryLabel} optionalLabel={TEXT.doneSummaryOptional} value={summary} onChange={setSummary} note={TEXT.doneSummaryNote} submitLabel={TEXT.doneSummaryAdd} skipLabel={TEXT.doneSummarySkip} onSubmit={() => void endShipped(true)} onSkip={() => void endShipped(false)} />
      )}
      <LaneBoard
        lanes={all}
        cardTitle={(id) => byId.get(id)?.title ?? id}
        onMove={move}
        readOnly={paused}
        labels={{ lane: TEXT.lane, empty: TEXT.laneEmpty, pickedUp: TEXT.pickedUp, movedOver: TEXT.movedOver, dropped: TEXT.dropped, cancelled: TEXT.cancelled, pending: TEXT.saving, dragHint: TEXT.dragHint, more: TEXT.laneMore, collapsed: TEXT.laneCollapsed, wasHere: TEXT.wasHere }}
      />
    </>
  )
}

function PromptSubject({ task }: { task?: ThemeTask }) {
  if (!task) return null
  return <span className="flex items-baseline gap-2"><span className="rounded-sm border border-border px-1.5 font-mono text-caption text-fg-secondary">{shortTaskId(task.id)}</span>{task.title}</span>
}

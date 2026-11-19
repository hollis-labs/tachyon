import { useState } from 'react'
import { ArrowDown, ArrowUp, ChevronDown, ChevronRight, GripVertical, Plus, X } from 'lucide-react'
import { CAPTION, Chip, Kbd, SegmentedProgress } from '@planner/shared'
import { cn } from '@hollis-labs/design-components'
import { Act } from '../cards/Block'
import type { Plan, PlanStep } from './data'

const ROW = 'flex items-center gap-2 rounded-control px-1.5 py-[5px] text-xs text-fg-secondary hover:bg-selection'
const PLAN_DRAG = 'application/x-chat-plan'
const STEP_DRAG = 'application/x-chat-step'
const ICON_BTN = 'grid size-[22px] shrink-0 place-items-center rounded-control text-fg-muted hover:bg-surface-hover hover:text-fg focus-visible:outline focus-visible:outline-1 focus-visible:outline-ring disabled:opacity-40'

const KEY = (ref?: string) => `console.plan.closed.${ref ?? 'none'}`
function readClosed(ref?: string): Record<string, boolean> {
  try { return JSON.parse(localStorage.getItem(KEY(ref)) ?? '{}') as Record<string, boolean> } catch { return {} }
}

export interface PlansPanelProps {
  plans: Plan[]
  loading: boolean
  error: string
  threadRef?: string
  setStatus: (plan: Plan, s: PlanStep, st: PlanStep['status']) => void
  addStep: (plan: Plan | null, title: string) => Promise<void>
  createPlan: (title: string) => Promise<void>
  deletePlan: (plan: Plan) => Promise<void>
  reorderPlans: (ids: string[]) => Promise<void>
  moveStep: (from: Plan, to: Plan, stepId: string, position?: number) => Promise<void>
  onAsk: () => void
}

/** A plan's name: its title, else "Todo list" for the loose one, else its place. */
const planName = (p: Plan, i: number) => p.title || (i === 0 ? 'Todo list' : `Plan ${i + 1}`)

/**
 * chat.aside Plan: every plan of the thread as an accordion (open state is remembered per thread). Plans reorder by
 * dragging the grip or with the arrow buttons; a step moves to another plan by dropping it on that plan or with its
 * "Move to" select. The server keeps the step's id, status and task link and checks both plans' revisions.
 */
export function PlanPanel({ plans, loading, error, threadRef, setStatus, addStep, createPlan, deletePlan, reorderPlans, moveStep, onAsk }: PlansPanelProps) {
  const [closed, setClosed] = useState<Record<string, boolean>>(() => readClosed(threadRef))
  const [adding, setAdding] = useState<string | null>(null)
  const [title, setTitle] = useState('')
  const [newPlan, setNewPlan] = useState(false)
  const [planTitle, setPlanTitle] = useState('')
  if (loading) return <p role="status" className="text-xs text-fg-muted">Loading the plan…</p>
  const toggle = (id: string) => setClosed((c) => { const n = { ...c, [id]: !c[id] }; try { localStorage.setItem(KEY(threadRef), JSON.stringify(n)) } catch { /* optional */ } return n })
  const live = plans.flatMap((p) => p.steps.filter((s) => s.status !== 'cancelled'))
  const count = (st: PlanStep['status']) => live.filter((s) => s.status === st).length
  const submit = async (plan: Plan | null) => { const t = title.trim(); if (!t) return; setTitle(''); setAdding(null); await addStep(plan, t) }
  const move = (id: string, by: -1 | 1) => {
    const ids = plans.map((p) => p.id)
    const i = ids.indexOf(id)
    const j = i + by
    if (j < 0 || j >= ids.length) return
    ;[ids[i], ids[j]] = [ids[j], ids[i]]
    void reorderPlans(ids)
  }
  const dropOn = (e: React.DragEvent, target: Plan) => {
    e.preventDefault()
    const planId = e.dataTransfer.getData(PLAN_DRAG)
    if (planId && planId !== target.id) {
      const ids = plans.map((p) => p.id).filter((id) => id !== planId)
      ids.splice(ids.indexOf(target.id), 0, planId)
      void reorderPlans(ids)
      return
    }
    const raw = e.dataTransfer.getData(STEP_DRAG)
    if (!raw) return
    const { plan: fromId, step } = JSON.parse(raw) as { plan: string; step: string }
    const from = plans.find((p) => p.id === fromId)
    if (from && from.id !== target.id) void moveStep(from, target, step)
  }
  const none = plans.length === 0 || (plans.length === 1 && plans[0].steps.length === 0 && adding !== plans[0].id)
  return (
    <>
      {none ? (
        <p className="flex flex-col items-start gap-2 text-xs text-fg-muted">No plan yet. Ask the agent to draft one.<Act onClick={onAsk}>Ask the agent to draft one</Act></p>
      ) : (
        <section className="flex flex-col gap-1.5">
          <span className={CAPTION}>{plans.length > 1 ? `Plans · ${plans.length}` : 'Plan · this session'}</span>
          <SegmentedProgress label={`${count('done')} of ${live.length} done`} items={[{ state: 'done', count: count('done') }, { state: 'doing', count: count('doing') }, { state: 'blocked', count: count('blocked') }, { state: 'todo', count: count('todo') }]} />
        </section>
      )}
      {plans.map((p, i) => {
        const steps = p.steps.filter((s) => s.status !== 'cancelled')
        const done = steps.filter((s) => s.status === 'done').length
        const open = !closed[p.id]
        const name = planName(p, i)
        return (
          <section
            key={p.id}
            aria-label={name}
            onDragOver={(e) => { if (plans.length > 1 && (e.dataTransfer.types.includes(PLAN_DRAG) || e.dataTransfer.types.includes(STEP_DRAG))) e.preventDefault() }}
            onDrop={(e) => dropOn(e, p)}
            className="flex flex-col gap-0.5 rounded-control border border-border-subtle"
          >
            <div className="flex items-center gap-1 px-1 py-1">
              {plans.length > 1 && (
                <span draggable aria-hidden onDragStart={(e) => { e.dataTransfer.setData(PLAN_DRAG, p.id); e.dataTransfer.effectAllowed = 'move' }} className={cn(ICON_BTN, 'cursor-grab')} title="Drag to reorder"><GripVertical className="size-3" /></span>
              )}
              <button type="button" aria-expanded={open} onClick={() => toggle(p.id)} className="flex min-w-0 flex-1 items-center gap-1.5 rounded-control px-1 py-0.5 text-left text-control font-medium text-fg hover:bg-selection focus-visible:outline focus-visible:outline-1 focus-visible:outline-ring">
                {open ? <ChevronDown aria-hidden className="size-3 shrink-0" /> : <ChevronRight aria-hidden className="size-3 shrink-0" />}
                <span className="min-w-0 flex-1 truncate">{name}</span>
                <small className="font-mono text-caption text-fg-faint">{done}/{steps.length}</small>
              </button>
              {plans.length > 1 && (
                <>
                  <button type="button" aria-label={`Move ${name} up`} disabled={i === 0} onClick={() => move(p.id, -1)} className={ICON_BTN}><ArrowUp aria-hidden className="size-3" /></button>
                  <button type="button" aria-label={`Move ${name} down`} disabled={i === plans.length - 1} onClick={() => move(p.id, 1)} className={ICON_BTN}><ArrowDown aria-hidden className="size-3" /></button>
                </>
              )}
              {p.steps.length === 0 && plans.length > 1 && <button type="button" aria-label={`Remove ${name}`} onClick={() => void deletePlan(p)} className={ICON_BTN}><X aria-hidden className="size-3" /></button>}
            </div>
            {open && (
              <div className="flex flex-col gap-0.5 px-2 pb-2">
                <ul className="flex flex-col gap-0.5">
                  {steps.map((s) => {
                    const isDone = s.status === 'done'
                    return (
                      <li key={s.id} draggable={plans.length > 1} onDragStart={(e) => { e.dataTransfer.setData(STEP_DRAG, JSON.stringify({ plan: p.id, step: s.id })); e.dataTransfer.effectAllowed = 'move' }} className={cn('group flex items-center gap-2 py-[3px] text-xs', isDone ? 'text-fg-muted line-through' : 'text-fg-secondary')}>
                        <button type="button" role="checkbox" aria-checked={isDone} aria-label={s.title} onClick={() => setStatus(p, s, isDone ? 'todo' : 'done')} className={cn('grid size-3 shrink-0 place-items-center rounded-sm border text-[9px] leading-none focus-visible:outline focus-visible:outline-1 focus-visible:outline-ring', isDone ? 'border-fg/60 bg-fg/60 text-bg' : s.status === 'doing' ? 'border-fg bg-fg/20' : 'border-fg-muted')}>{isDone ? '✓' : ''}</button>
                        <span className="min-w-0 flex-1 truncate">{s.title}</span>
                        {s.status === 'doing' && <Chip>doing</Chip>}
                        {s.status === 'blocked' && <Chip tone="danger">blocked</Chip>}
                        {plans.length > 1 && (
                          <select aria-label={`Move ${s.title} to another plan`} value="" onChange={(e) => { const to = plans.find((x) => x.id === e.target.value); if (to) void moveStep(p, to, s.id) }} className="h-5 w-[70px] shrink-0 rounded-control border border-border bg-bg px-1 text-caption text-fg-secondary opacity-0 focus-visible:opacity-100 group-focus-within:opacity-100 group-hover:opacity-100">
                            <option value="">Move to…</option>
                            {plans.map((x, xi) => x.id !== p.id && <option key={x.id} value={x.id}>{planName(x, xi)}</option>)}
                          </select>
                        )}
                      </li>
                    )
                  })}
                </ul>
                {adding === p.id ? (
                  <form className="flex items-center gap-2" onSubmit={(e) => { e.preventDefault(); void submit(p) }}>
                    <input autoFocus aria-label="New step" value={title} onChange={(e) => setTitle(e.target.value)} onKeyDown={(e) => { if (e.key === 'Escape') { setAdding(null); setTitle('') } }} placeholder="Add a step…" className="h-7 min-w-0 flex-1 rounded-control border border-border bg-bg px-2 text-xs focus-visible:bg-bg!" />
                    <Act type="submit" primary>Add</Act>
                  </form>
                ) : (
                  <button type="button" data-add-step onClick={() => { setTitle(''); setAdding(p.id) }} className={cn(ROW, 'text-fg-muted')}><Plus aria-hidden className="size-3" />Add a step{i === 0 && <Kbd className="ml-auto">A</Kbd>}</button>
                )}
              </div>
            )}
          </section>
        )
      })}
      {plans.length === 0 && (
        adding === 'new' ? (
          <form className="flex items-center gap-2" onSubmit={(e) => { e.preventDefault(); void submit(null) }}>
            <input autoFocus aria-label="New step" value={title} onChange={(e) => setTitle(e.target.value)} onKeyDown={(e) => { if (e.key === 'Escape') { setAdding(null); setTitle('') } }} placeholder="Add a step…" className="h-7 min-w-0 flex-1 rounded-control border border-border bg-bg px-2 text-xs focus-visible:bg-bg!" />
            <Act type="submit" primary>Add</Act>
          </form>
        ) : (
          <button type="button" data-add-step onClick={() => { setTitle(''); setAdding('new') }} className={cn(ROW, 'text-fg-muted')}><Plus aria-hidden className="size-3" />Add a step<Kbd className="ml-auto">A</Kbd></button>
        )
      )}
      {plans.length > 0 && (newPlan ? (
        <form className="flex items-center gap-2" onSubmit={(e) => { e.preventDefault(); const t = planTitle.trim(); setPlanTitle(''); setNewPlan(false); void createPlan(t) }}>
          <input autoFocus aria-label="New plan title" value={planTitle} onChange={(e) => setPlanTitle(e.target.value)} onKeyDown={(e) => { if (e.key === 'Escape') { setNewPlan(false); setPlanTitle('') } }} placeholder="Plan title…" className="h-7 min-w-0 flex-1 rounded-control border border-border bg-bg px-2 text-xs focus-visible:bg-bg!" />
          <Act type="submit" primary>Add plan</Act>
        </form>
      ) : (
        <button type="button" onClick={() => setNewPlan(true)} className={cn(ROW, 'text-fg-muted')}><Plus aria-hidden className="size-3" />Add a plan</button>
      ))}
      {error ? <p role="alert" className="text-xs text-danger">{error}</p> : null}
      {plans.length > 0 && plans.length < 2 && plans[0].task_ref && (
        <section className="flex flex-col gap-1.5">
          <span className={CAPTION}>Project</span>
          <div className={ROW}><Chip square>{plans[0].task_ref}</Chip><span className="min-w-0 flex-1 truncate">{plans[0].title ?? 'Linked work'}</span></div>
        </section>
      )}
    </>
  )
}

// Workflow card kinds (H2). Compact (tray/toast row), inline (one line) and
// expanded (interactive). Nothing here auto-applies: every control is an
// explicit click and anything that authorises a change goes through the
// ActionBus with IdentityConfirm (declared on the action spec in model.ts).
import { useId, useState } from 'react'
import { CardFrame } from '../cards/frame'
import type { CardActionSpec, CardRenderProps } from '../cards/types'
import { AuditTrail, Btn, ContextLinks, FIELD, StatusLine } from '../hitl/parts'
import { checkpointAction, gateAction, planAction, runFromCard, runProgress, steerAction, STATE_LABEL } from './model'
import { PRIORITIES, type RunPriority, type WorkflowGate, type WorkflowPhase, type WorkflowRun } from './types'

const LABEL = { progress: 'Workflow', steer: 'Steer', gate: 'Gate', plan: 'Plan', checkpoint: 'Checkpoint', decision: 'Decision' } as const

const STATE_TEXT: Record<WorkflowRun['state'], string> = {
  running: 'text-success',
  paused: 'text-info',
  blocked: 'text-warning',
  awaiting_plan: 'text-info',
  completed: 'text-fg-secondary',
  failed: 'text-danger',
  cancelled: 'text-fg-muted',
}

const PHASE_MARK: Record<WorkflowPhase['status'], string> = {
  pending: 'bg-surface-hover border border-border',
  active: 'bg-warning',
  passed: 'bg-success',
  failed: 'bg-danger',
  skipped: 'bg-surface',
}

const busy = (p: CardRenderProps) => Object.values(p.controller.status).some((x) => x.pending)
const live = (p: CardRenderProps) => p.card.phase === 'live'

function StateChip({ run }: { run: WorkflowRun }) {
  return <span data-run-state={run.state} className={`font-mono text-label uppercase tracking-label ${STATE_TEXT[run.state]}`}>{STATE_LABEL[run.state]}</span>
}

function Unreadable({ p, kind }: { p: CardRenderProps; kind: keyof typeof LABEL }) {
  return (
    <CardFrame card={p.card} density={p.density === 'inline' ? 'compact' : p.density} kindLabel={LABEL[kind]} onExpand={p.onExpand}>
      <p className="mt-1 font-mono text-label text-danger">Run data could not be read.</p>
    </CardFrame>
  )
}

function InlineLine({ p, run, kind }: { p: CardRenderProps; run: WorkflowRun; kind: keyof typeof LABEL }) {
  return (
    <span data-card-kind={p.card.kind} data-card-phase={p.card.phase} className="inline-flex items-center gap-2 font-mono text-label text-fg-secondary">
      <span aria-hidden className={`h-1.5 w-1.5 ${p.card.phase === 'live' ? 'bg-warning' : 'bg-fg-faint'}`} />
      {LABEL[kind]}: {p.card.title}
      <StateChip run={run} />
    </span>
  )
}

function Meta({ run }: { run: WorkflowRun }) {
  return (
    <div className="mt-1 flex flex-wrap items-center gap-x-3 gap-y-1 font-mono text-label text-fg-secondary">
      <StateChip run={run} />
      <span>agent <span className="text-fg">{run.agent.label ?? run.agent.id}</span></span>
      <span>priority <span className="text-fg">{run.priority}</span></span>
      {run.escalatedTo && <span className="text-warning">escalated to {run.escalatedTo}</span>}
    </div>
  )
}

function Frame({ p, run, kind, children, audit = true }: { p: CardRenderProps; run: WorkflowRun; kind: keyof typeof LABEL; children?: React.ReactNode; audit?: boolean }) {
  const compact = p.density === 'compact'
  return (
    <CardFrame card={p.card} density={p.density} kindLabel={LABEL[kind]} onExpand={p.onExpand} footer={compact || !audit ? undefined : <AuditTrail audit={run.audit} />}>
      <Meta run={run} />
      {!compact && <ContextLinks links={run.subjects.filter((s) => s.type !== 'run')} />}
      {!compact && children}
    </CardFrame>
  )
}

function run1(p: CardRenderProps, a: CardActionSpec) {
  void p.controller.run(a)
}

/* ------------------------------- progress ------------------------------- */

export function GateChip({ gate }: { gate: WorkflowGate }) {
  const tone = gate.status === 'open' ? 'border-warning/70 text-warning' : gate.status === 'failed' ? 'border-danger/70 text-danger' : 'border-success/50 text-success'
  return <span data-gate-status={gate.status} className={`border px-1 py-px font-mono text-micro uppercase tracking-label ${tone}`}>gate {gate.status}</span>
}

function PhaseRow({ ph }: { ph: WorkflowPhase }) {
  const pct = ph.progress && ph.progress.total > 0 ? Math.round((ph.progress.done / ph.progress.total) * 100) : undefined
  return (
    <li data-phase-status={ph.status} className="grid grid-cols-[0.75rem_1fr_auto] items-center gap-x-3 border-b border-border-subtle py-1.5 last:border-b-0">
      <span aria-hidden className={`h-2 w-2 ${PHASE_MARK[ph.status]}`} />
      <div className="min-w-0">
        <span className={`text-sm ${ph.status === 'pending' ? 'text-fg-muted' : 'text-fg'}`}>{ph.label}</span>
        {pct !== undefined && ph.status === 'active' && (
          <div className="mt-1 flex items-center gap-2">
            <div role="progressbar" aria-label={`${ph.label} progress`} aria-valuenow={pct} aria-valuemin={0} aria-valuemax={100} className="h-1 w-32 bg-surface-hover">
              <div className="h-full bg-warning" style={{ width: `${pct}%` }} />
            </div>
            <span className="font-mono text-micro text-fg-muted">{ph.progress!.done}/{ph.progress!.total}</span>
          </div>
        )}
      </div>
      <div className="flex items-center gap-2">
        {ph.gate && <GateChip gate={ph.gate} />}
        <span className="w-14 text-right font-mono text-micro uppercase tracking-label text-fg-muted">{ph.status}</span>
      </div>
    </li>
  )
}

export function WorkflowProgressCard(p: CardRenderProps) {
  const run = runFromCard(p.card)
  if (!run) return <Unreadable p={p} kind="progress" />
  if (p.density === 'inline') return <InlineLine p={p} run={run} kind="progress" />
  const pr = runProgress(run)
  return (
    <Frame p={p} run={run} kind="progress">
      <div className="mt-2 flex items-center gap-2 font-mono text-label text-fg-secondary">
        <span className="text-fg">{pr.passed}/{pr.total}</span> phases complete
      </div>
      <ol aria-label="Phases" className="mt-2 border-t border-border-subtle">{run.phases.map((ph) => <PhaseRow key={ph.id} ph={ph} />)}</ol>
      {run.redirect && <p className="mt-2 font-mono text-label text-fg-muted">last redirect: <span className="text-fg-secondary">{run.redirect}</span></p>}
    </Frame>
  )
}

/* -------------------------------- steering -------------------------------- */

export function WorkflowSteerCard(p: CardRenderProps) {
  const run = runFromCard(p.card)
  const [mode, setMode] = useState<'redirect' | 'escalate' | null>(null)
  const [instruction, setInstruction] = useState('')
  const [target, setTarget] = useState('owner')
  const [prio, setPrio] = useState<RunPriority | ''>('')
  const rid = useId()
  if (!run) return <Unreadable p={p} kind="steer" />
  if (p.density === 'inline') return <InlineLine p={p} run={run} kind="steer" />
  const off = busy(p) || !live(p)
  const paused = run.state === 'paused'
  const resumable = paused || run.state === 'blocked'
  return (
    <Frame p={p} run={run} kind="steer">
      {run.blockedReason && <p className="mt-2 text-xs text-warning">Blocked: {run.blockedReason}</p>}
      {live(p) && (
        <div className="mt-3">
          <div className="flex flex-wrap items-center gap-2">
            {resumable ? (
              <Btn intent="primary" disabled={off} onClick={() => run1(p, steerAction(run, { type: 'resume' }))}>Resume</Btn>
            ) : (
              <Btn disabled={off} onClick={() => run1(p, steerAction(run, { type: 'pause' }))}>Pause</Btn>
            )}
            <Btn disabled={off} aria-pressed={mode === 'redirect'} onClick={() => setMode(mode === 'redirect' ? null : 'redirect')}>Redirect*</Btn>
            <Btn intent="danger" disabled={off} aria-pressed={mode === 'escalate'} onClick={() => setMode(mode === 'escalate' ? null : 'escalate')}>Escalate</Btn>
            <label className="ml-auto flex items-center gap-1.5 font-mono text-micro uppercase tracking-label text-fg-muted">
              priority*
              <select aria-label="Priority" value={prio || run.priority} disabled={off} onChange={(e) => setPrio(e.target.value as RunPriority)} className={`${FIELD} !w-auto !py-0.5 font-mono text-label`}>
                {PRIORITIES.map((x) => <option key={x} value={x}>{x}</option>)}
              </select>
            </label>
            {prio && prio !== run.priority && <Btn intent="primary" disabled={off} onClick={() => run1(p, steerAction(run, { type: 'priority', priority: prio }))}>Apply</Btn>}
          </div>
          {mode === 'redirect' && (
            <div className="mt-2">
              <label htmlFor={rid} className="font-mono text-micro uppercase tracking-label text-fg-muted">New instruction for {run.agent.label ?? run.agent.id}</label>
              <textarea id={rid} rows={3} value={instruction} onChange={(e) => setInstruction(e.target.value)} disabled={off} className={`${FIELD} mt-1`} />
              <div className="pt-2"><Btn intent="primary" disabled={off || !instruction.trim()} onClick={() => { run1(p, steerAction(run, { type: 'redirect', instruction: instruction.trim() })); setInstruction('') }}>Send redirect</Btn></div>
            </div>
          )}
          {mode === 'escalate' && (
            <div className="mt-2 flex items-end gap-2">
              <label className="font-mono text-micro uppercase tracking-label text-fg-muted">
                Escalate to
                <input aria-label="Escalate to" value={target} onChange={(e) => setTarget(e.target.value)} disabled={off} className={`${FIELD} mt-1`} />
              </label>
              <Btn intent="danger" disabled={off || !target.trim()} onClick={() => run1(p, steerAction(run, { type: 'escalate', to: target.trim() }))}>Escalate now</Btn>
            </div>
          )}
          <div className="pt-2"><StatusLine controller={p.controller} ids={['pause', 'resume', 'redirect', 'escalate', 'priority']} /></div>
          <p className="pt-1 font-mono text-micro text-fg-muted">* changing what the agent does asks you to confirm your identity first</p>
        </div>
      )}
      {!live(p) && <p className="mt-2 font-mono text-label text-fg-muted">Run is {STATE_LABEL[run.state].toLowerCase()}; steering is closed.</p>}
    </Frame>
  )
}

/* ---------------------------------- gate ---------------------------------- */

export function WorkflowGateCard(p: CardRenderProps) {
  const run = runFromCard(p.card)
  const [note, setNote] = useState('')
  const nid = useId()
  if (!run) return <Unreadable p={p} kind="gate" />
  if (p.density === 'inline') return <InlineLine p={p} run={run} kind="gate" />
  const gate = run.phases.find((x) => x.gate?.id === p.card.props.gateId)?.gate
  if (!gate) return <Unreadable p={p} kind="gate" />
  const off = busy(p) || !live(p)
  return (
    <Frame p={p} run={run} kind="gate">
      <div className="mt-2 flex items-center gap-2"><GateChip gate={gate} /></div>
      {gate.criteria.length > 0 && (
        <ul aria-label="Gate criteria" className="mt-2 border border-border-subtle">
          {gate.criteria.map((c, i) => (
            <li key={i} data-criterion-met={c.met} className="flex items-center gap-2 border-b border-border-subtle px-3 py-1.5 text-xs last:border-b-0">
              <span aria-hidden className={`h-2 w-2 ${c.met ? 'bg-success' : 'bg-danger'}`} />
              <span className="text-fg">{c.label}</span>
              <span className="ml-auto font-mono text-micro uppercase tracking-label text-fg-muted">{c.met ? 'met' : 'not met'}</span>
            </li>
          ))}
        </ul>
      )}
      {live(p) ? (
        <div className="mt-3">
          <label htmlFor={nid} className="font-mono text-micro uppercase tracking-label text-fg-muted">Note (required to override)</label>
          <textarea id={nid} rows={2} value={note} onChange={(e) => setNote(e.target.value)} disabled={off} className={`${FIELD} mt-1`} />
          <div className="flex flex-wrap items-center gap-2 pt-2">
            <Btn intent="primary" disabled={off} onClick={() => run1(p, gateAction(run, gate, 'pass', note || undefined))}>Pass gate*</Btn>
            <Btn intent="danger" disabled={off} onClick={() => run1(p, gateAction(run, gate, 'fail', note || undefined))}>Fail gate</Btn>
            <Btn disabled={off || !note.trim()} onClick={() => run1(p, gateAction(run, gate, 'override', note))}>Override*</Btn>
            <StatusLine controller={p.controller} ids={['pass', 'fail', 'override']} />
          </div>
          <p className="pt-1.5 font-mono text-micro text-fg-muted">* passing or overriding asks you to confirm your identity first</p>
        </div>
      ) : (
        <p className="mt-2 font-mono text-label text-success">{p.card.resolution?.label ?? 'Closed'}</p>
      )}
    </Frame>
  )
}

/* ---------------------------------- plan ---------------------------------- */

export function WorkflowPlanCard(p: CardRenderProps) {
  const run = runFromCard(p.card)
  const [dropped, setDropped] = useState<string[]>([])
  const [note, setNote] = useState('')
  const nid = useId()
  if (!run?.plan) return <Unreadable p={p} kind="plan" />
  if (p.density === 'inline') return <InlineLine p={p} run={run} kind="plan" />
  const plan = run.plan
  const off = busy(p) || !live(p)
  const included = plan.items.filter((i) => (live(p) ? !dropped.includes(i.id) : i.included)).map((i) => i.id)
  const edited = included.length !== plan.items.length
  return (
    <Frame p={p} run={run} kind="plan">
      <ul aria-label="Proposed phases" className="mt-2 border border-border-subtle">
        {plan.items.map((i) => {
          const on = included.includes(i.id)
          return (
            <li key={i.id} className="border-b border-border-subtle last:border-b-0">
              <label className={`flex items-start gap-2.5 px-3 py-2 ${live(p) ? 'cursor-pointer hover:bg-surface' : ''}`}>
                <input type="checkbox" checked={on} disabled={off} onChange={() => setDropped(on ? [...dropped, i.id] : dropped.filter((x) => x !== i.id))} className="mt-0.5" />
                <span><span className={`text-sm ${on ? 'text-fg' : 'text-fg-muted line-through'}`}>{i.label}</span>{i.summary && <span className="block text-xs text-fg-secondary">{i.summary}</span>}</span>
              </label>
            </li>
          )
        })}
      </ul>
      {live(p) ? (
        <div className="mt-3">
          <label htmlFor={nid} className="font-mono text-micro uppercase tracking-label text-fg-muted">Note (optional, recorded in the audit)</label>
          <textarea id={nid} rows={2} value={note} onChange={(e) => setNote(e.target.value)} disabled={off} className={`${FIELD} mt-1`} />
          <div className="flex flex-wrap items-center gap-2 pt-2">
            <Btn intent="primary" disabled={off || included.length === 0} onClick={() => run1(p, planAction(run, 'approve', included, note || undefined))}>
              {edited ? `Approve ${included.length} of ${plan.items.length}` : 'Approve plan'}*
            </Btn>
            <Btn intent="danger" disabled={off} onClick={() => run1(p, planAction(run, 'reject', [], note || undefined))}>Reject plan</Btn>
            <StatusLine controller={p.controller} ids={['approve', 'reject']} />
          </div>
          <p className="pt-1.5 font-mono text-micro text-fg-muted">* approving asks you to confirm your identity first; untick phases to edit the plan</p>
        </div>
      ) : (
        <p className="mt-2 font-mono text-label text-fg-secondary">{p.card.resolution?.label}{plan.note ? ` - ${plan.note}` : ''}</p>
      )}
    </Frame>
  )
}

/* ------------------------------- checkpoint ------------------------------- */

export function WorkflowCheckpointCard(p: CardRenderProps) {
  const run = runFromCard(p.card)
  const [picked, setPicked] = useState('')
  const [text, setText] = useState('')
  const gid = useId()
  const cp = run?.checkpoint
  if (!run || !cp) return <Unreadable p={p} kind="checkpoint" />
  if (p.density === 'inline') return <InlineLine p={p} run={run} kind="checkpoint" />
  const off = busy(p) || !live(p)
  const answer = text.trim() || picked
  return (
    <Frame p={p} run={run} kind="checkpoint">
      {live(p) ? (
        <fieldset className="mt-3" disabled={off}>
          {cp.options.length > 0 && (
            <div className="border border-border-subtle">
              <legend className="sr-only">Options</legend>
              {cp.options.map((o) => (
                <label key={o.value} className={`flex cursor-pointer items-start gap-2.5 border-b border-border-subtle px-3 py-2 last:border-b-0 hover:bg-surface ${picked === o.value ? 'bg-surface' : ''}`}>
                  <input type="radio" name={gid} checked={picked === o.value && !text.trim()} onChange={() => { setPicked(o.value); setText('') }} className="mt-0.5" />
                  <span><span className="text-sm text-fg">{o.label}</span>{o.description && <span className="block text-xs text-fg-secondary">{o.description}</span>}</span>
                </label>
              ))}
            </div>
          )}
          {cp.allowText && <textarea aria-label="Answer" rows={2} placeholder={cp.options.length ? 'Or write your own answer' : 'Type your answer'} value={text} onChange={(e) => setText(e.target.value)} className={`${FIELD} mt-2`} />}
          <div className="flex items-center gap-2 pt-2">
            <Btn intent="primary" disabled={!answer} onClick={() => run1(p, checkpointAction(run, cp.id, answer))}>Send answer</Btn>
            <StatusLine controller={p.controller} ids={['answer']} />
          </div>
        </fieldset>
      ) : (
        <p className="mt-2 font-mono text-label text-success">{cp.status === 'answered' ? `Answered: ${cp.answer ?? ''}` : 'Cancelled'}</p>
      )}
    </Frame>
  )
}

/* ------------------------- decision (flow step) ------------------------- */

interface FlowProps { input?: { label: string; placeholder?: string; required?: boolean } }

export function WorkflowDecisionCard(p: CardRenderProps) {
  const [input, setInput] = useState('')
  const iid = useId()
  const flow = (p.card.props.flow ?? {}) as FlowProps
  if (p.density === 'inline') {
    return (
      <span data-card-kind={p.card.kind} data-card-phase={p.card.phase} className="inline-flex items-center gap-2 font-mono text-label text-fg-secondary">
        <span aria-hidden className={`h-1.5 w-1.5 ${p.card.phase === 'live' ? 'bg-warning' : 'bg-fg-faint'}`} />
        {LABEL.decision}: {p.card.title}
      </span>
    )
  }
  const off = busy(p) || !live(p)
  const compact = p.density === 'compact'
  return (
    <CardFrame card={p.card} density={p.density} kindLabel={LABEL.decision} onExpand={p.onExpand}>
      {!compact && live(p) && (
        <div className="mt-3">
          {flow.input && (
            <>
              <label htmlFor={iid} className="font-mono text-micro uppercase tracking-label text-fg-muted">{flow.input.label}{flow.input.required ? ' (required)' : ''}</label>
              <textarea id={iid} rows={3} value={input} placeholder={flow.input.placeholder} onChange={(e) => setInput(e.target.value)} disabled={off} className={`${FIELD} mt-1`} />
            </>
          )}
          <div className="flex flex-wrap items-center gap-2 pt-2">
            {p.card.actions.map((a) => (
              <Btn key={a.id} intent={a.intent} disabled={off || (!!flow.input?.required && a.id !== 'back' && !input.trim() && a.intent === 'primary')} onClick={() => run1(p, { ...a, params: { ...a.params, input } })}>
                {p.controller.status[a.id]?.pending ? `${a.label}...` : a.label}
              </Btn>
            ))}
            <StatusLine controller={p.controller} ids={p.card.actions.map((a) => a.id)} />
          </div>
        </div>
      )}
      {!compact && !live(p) && <p className="mt-2 font-mono text-label text-fg-secondary">{p.card.resolution?.label}</p>}
    </CardFrame>
  )
}

import type { ReactNode } from 'react'
import { Block, Btn, Chip, RiskBadge, StatusBadge } from './ui'
import { clock, RISK_STYLE } from './ui-util'
import type { LifecycleItem, RetireImpact } from './lifecycle'
import type { AgentPlan, ApplyOutcome, FieldChange, FieldValue, ValidationIssue } from './plan'

const show = (v: FieldValue) => (typeof v === 'boolean' ? (v ? 'on' : 'off') : v === '' ? '(empty)' : String(v))

function ChangeRow({ c }: { c: FieldChange }) {
  return (
    <li className="border-l-2 border-warning/70 bg-panel/40 px-3 py-2">
      <div className="font-mono dash-label uppercase tracking-wider text-fg-muted">{c.label}</div>
      {c.kind === 'list' ? (
        <div className="mt-1 flex flex-wrap gap-1.5">
          {c.added?.map((x) => <Chip key={`+${x}`} tone="border-success/50 text-success">+ {x}</Chip>)}
          {c.removed?.map((x) => <Chip key={`-${x}`} tone="border-danger/50 text-danger line-through">- {x}</Chip>)}
        </div>
      ) : (
        <div className="mt-1 grid gap-1 dash-body sm:grid-cols-2">
          <div className="min-w-0 break-words border border-danger/30 bg-danger/5 px-2 py-1 font-mono dash-support text-danger"><span aria-hidden>- </span>{show(c.before)}</div>
          <div className="min-w-0 break-words border border-success/30 bg-success/5 px-2 py-1 font-mono dash-support text-success"><span aria-hidden>+ </span>{show(c.after)}</div>
        </div>
      )}
    </li>
  )
}

export function IssueList({ issues }: { issues: ValidationIssue[] }) {
  if (!issues.length) return <p className="dash-body text-success">No validation problems.</p>
  return (
    <ul className="space-y-1">
      {issues.map((i, n) => (
        <li key={n} className={`flex gap-2 dash-body ${i.level === 'error' ? 'text-danger' : 'text-warning'}`}>
          <span className="w-14 shrink-0 font-mono dash-caption uppercase tracking-widest">{i.level}</span>
          <span>{i.message}</span>
        </li>
      ))}
    </ul>
  )
}

const ITEM_TONE: Record<LifecycleItem['tone'], { mark: string; cls: string }> = {
  add: { mark: '+', cls: 'border-success/40 bg-success/5 text-success' },
  remove: { mark: '-', cls: 'border-danger/40 bg-danger/5 text-danger' },
  change: { mark: '~', cls: 'border-warning/40 bg-warning/5 text-warning' },
  note: { mark: 'i', cls: 'border-border bg-panel/40 text-fg-muted' },
}

/** Impact counts for a retire; shared by the plan review and the retire panel. */
export function ImpactSummary({ impact }: { impact: RetireImpact }) {
  const cells: [string, number][] = [['sessions', impact.sessions.count], ['tasks', impact.tasks.length], ['workstreams', impact.workstreams.length], ['teams', impact.teams.length]]
  return (
    <dl aria-label="Impact" className="grid grid-cols-2 gap-px border border-border bg-panel-2 sm:grid-cols-4">
      {cells.map(([k, n]) => (
        <div key={k} className="bg-bg px-3 py-2">
          <dd className={`font-mono dash-metric-value ${n > 0 ? 'text-warning' : 'text-fg-faint'}`}>{n}</dd>
          <dt className="font-mono dash-caption uppercase tracking-widest text-fg-faint">{k}</dt>
        </div>
      ))}
    </dl>
  )
}

export function PlanDiff({ plan }: { plan: AgentPlan }) {
  const lc = plan.lifecycle
  const sections = [...new Set(plan.changes.map((c) => c.section))]
  return (
    <>
      <Block title="Risk">
        {plan.risks.length === 0 ? (
          <p className="dash-body text-fg-muted">No privileged changes.</p>
        ) : (
          <ul className="space-y-1.5">
            {plan.risks.map((r) => (
              <li key={r.id} className="flex flex-wrap items-baseline gap-x-3 dash-body">
                <span className={`w-16 shrink-0 border px-1 text-center font-mono dash-caption uppercase tracking-widest ${RISK_STYLE[r.level]}`}>{r.level}</span>
                <span className="text-fg">{r.title}</span>
                <span className="text-fg-faint">{r.detail}</span>
              </li>
            ))}
          </ul>
        )}
      </Block>
      {lc && (
        <Block title={lc.summary}>
          {lc.impact && <div className="mb-3"><ImpactSummary impact={lc.impact} /></div>}
          <ul className="space-y-1.5">
            {lc.items.map((it, n) => (
              <li key={n} className={`flex gap-3 border-l-2 px-3 py-1.5 dash-body ${ITEM_TONE[it.tone].cls}`}>
                <span aria-hidden className="w-3 shrink-0 font-mono">{ITEM_TONE[it.tone].mark}</span>
                <span className="min-w-0 break-words"><span className="text-fg">{it.label}</span>{it.detail && <span className="ml-2 text-fg-muted">{it.detail}</span>}</span>
              </li>
            ))}
          </ul>
          {plan.note && <p className="mt-3 border-t border-border/80 pt-2 dash-body text-fg-muted"><span className="font-mono dash-caption uppercase tracking-widest text-fg-faint">reason </span>{plan.note}</p>}
        </Block>
      )}
      <Block title="Validation">
        <IssueList issues={plan.issues} />
      </Block>
      {(!lc || plan.changes.length > 0) && <Block title={`Changes (${plan.changes.length})`}>
        {plan.changes.length === 0 ? (
          <p className="dash-body text-fg-faint">Nothing differs from the current definition.</p>
        ) : (
          <div className="space-y-4">
            {sections.map((s) => (
              <div key={s}>
                <div className="mb-1 font-mono dash-caption uppercase tracking-widest text-fg-faint">{s}</div>
                <ul className="space-y-1.5">{plan.changes.filter((c) => c.section === s).map((c) => <ChangeRow key={c.field} c={c} />)}</ul>
              </div>
            ))}
          </div>
        )}
        {plan.note && !lc && <p className="mt-3 border-t border-border/80 pt-2 dash-body text-fg-muted"><span className="font-mono dash-caption uppercase tracking-widest text-fg-faint">reason </span>{plan.note}</p>}
      </Block>}
    </>
  )
}

export interface PlanReviewProps {
  plan: AgentPlan
  outcome: ApplyOutcome | null
  busy: boolean
  message: string | null
  onBack: () => void
  onApprove: () => void
  onApply: () => void
  onRollback: () => void
  onDemonstrate?: () => void
  onDone: () => void
}

export function PlanReview({ plan, outcome, busy, message, onBack, onApprove, onApply, onRollback, onDemonstrate, onDone }: PlanReviewProps) {
  const done = ['demonstrated', 'applied', 'failed', 'conflict', 'expired', 'rolled_back'].includes(plan.status)
  let banner: ReactNode = null
  if (done) {
    const ok = plan.status === 'demonstrated' || plan.status === 'applied' || plan.status === 'rolled_back'
    const text =
      plan.status === 'demonstrated' ? outcome?.message ?? 'Demonstration save succeeded (no-op). Nothing was written; write access is pending.'
      : plan.status === 'applied' ? 'Changes applied.'
      : plan.status === 'rolled_back' ? 'Changes rolled back. The previous state is restored.'
      : plan.status === 'conflict' ? 'Not applied: the definition changed since this plan was made.'
      : plan.status === 'expired' ? 'Not applied: the approval expired.'
      : 'Apply failed. Nothing changed.'
    banner = (
      <div role="status" className={`border-b px-5 py-3 dash-body ${ok ? 'border-success/30 bg-success/5 text-success' : 'border-danger/30 bg-danger/5 text-danger'}`}>
        <div className="font-medium">{text}</div>
        {plan.status !== 'demonstrated' && outcome?.message && <div className="mt-0.5 text-fg-muted">{outcome.message}</div>}
        {outcome?.revisionAfter && <div className="mt-0.5 font-mono dash-support text-fg-muted">revision {outcome.revisionBefore || 'unknown'} → {outcome.revisionAfter}{outcome.auditId ? ` · audit ${outcome.auditId}` : ''}</div>}
      </div>
    )
  }
  return (
    <div aria-label="Change plan">
      <div className="flex flex-wrap items-center gap-3 border-b border-border/80 px-5 py-3">
        <span className="font-mono dash-label uppercase tracking-widest text-fg-faint">plan {plan.id}</span>
        <StatusBadge status={plan.status} />
        <RiskBadge risk={plan.risk} />
        <span className="ml-auto font-mono dash-label text-fg-faint">base {plan.baseRevision || 'unknown'} · {clock(plan.createdAt)}</span>
      </div>
      {banner}
      {message && !done && <div role="alert" className="border-b border-danger/30 bg-danger/5 px-5 py-2 dash-body text-danger">{message}</div>}
      {plan.approval && plan.status === 'approved' && (
        <div className="border-b border-success/30 bg-success/5 px-5 py-2 dash-body text-success">
          Approved by {plan.approval.by.label ?? plan.approval.by.id} at {clock(plan.approval.at)}. Apply before {clock(plan.approval.expiresAt)}.
        </div>
      )}
      <PlanDiff plan={plan} />
      <div className="flex flex-wrap items-center gap-2 px-5 py-3">
        {plan.status === 'proposed' && (<><Btn onClick={onBack} disabled={busy}>Back to edit</Btn>{plan.demonstration ? <Btn tone="primary" onClick={onDemonstrate} disabled={busy || !onDemonstrate}>Save demonstration</Btn> : <Btn tone="primary" onClick={onApprove} disabled={busy}>Approve…</Btn>}</>)}
        {plan.status === 'invalid' && <Btn onClick={onBack}>Back to edit</Btn>}
        {plan.status === 'approved' && (<><Btn onClick={onBack} disabled={busy}>Back to edit</Btn><Btn tone="primary" onClick={onApply} disabled={busy}>Apply…</Btn></>)}
        {plan.status === 'applied' && (<><Btn tone="danger" onClick={onRollback} disabled={busy}>Roll back…</Btn><Btn tone="primary" onClick={onDone}>Done</Btn></>)}
        {(plan.status === 'failed' || plan.status === 'conflict' || plan.status === 'expired') && (<><Btn onClick={onBack}>Re-plan</Btn><Btn onClick={onDone}>Close</Btn></>)}
        {plan.status === 'demonstrated' && <><Btn onClick={onBack}>Back to edit</Btn><Btn tone="primary" onClick={onDone}>Done</Btn></>}
        {plan.status === 'rolled_back' && <Btn tone="primary" onClick={onDone}>Done</Btn>}
        {busy && <span className="font-mono dash-label uppercase tracking-widest text-fg-faint">working…</span>}
      </div>
    </div>
  )
}

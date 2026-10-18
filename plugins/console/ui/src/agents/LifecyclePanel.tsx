import { AdminInput, AdminSelect } from '@hollis-labs/kit-admin'
import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { useAgentsClient } from './context'
import { Field, inputCls } from './AgentEditor'
import { impactTotal, type RetireImpact, type RetireSessions } from './lifecycle'
import type { AgentPlan } from './plan'
import { ImpactSummary } from './PlanReview'
import { PlanRunner } from './PlanRunner'
import { Block, Btn, RiskBadge } from './ui'
import type { AgentRecord } from './types'

export interface LifecyclePanelProps {
  agent: AgentRecord
  /** Open the create wizard preloaded with this agent as the clone source. */
  onClone: (agentId: string) => void
  /** The agent record changed (retired or restored). */
  onChanged: (agent: AgentRecord) => void
}

/** Clone, retire (with impact preview) and restore for one agent. Retire and restore are high-risk plans that need a written reason. */
export function LifecyclePanel({ agent, onClone, onChanged }: LifecyclePanelProps) {
  const client = useAgentsClient()
  const retired = !!agent.identity.retired
  const id = agent.identity.id
  const [impact, setImpact] = useState<RetireImpact | null>(null)
  const [sessions, setSessions] = useState<RetireSessions>('drain')
  const [note, setNote] = useState('')
  const [plan, setPlan] = useState<AgentPlan | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    let active = true
    if (!retired && client.writeMode !== 'demonstration') client.getRetireImpact(id).then((i) => active && setImpact(i), (e) => active && setError(e instanceof Error ? e.message : 'Impact unavailable'))
    return () => {
      active = false
    }
  }, [client, id, retired, agent.source.revision])

  const make = async () => {
    setBusy(true)
    setError(null)
    try {
      setPlan(await client.planLifecycle(retired ? { op: 'agent.restore', agentId: id, note, baseRevision: agent.source.revision ?? '' } : { op: 'agent.retire', agentId: id, sessions, note, baseRevision: agent.source.revision ?? '' }))
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Could not plan')
    } finally {
      setBusy(false)
    }
  }

  if (plan) {
    return (
      <article aria-label="Lifecycle plan">
        <PlanRunner
          plan={plan}
          subjectLabel={agent.identity.name}
          onBack={() => setPlan(null)}
          onDone={() => { setPlan(null); setNote('') }}
          onChanged={(o) => o.agent && onChanged(o.agent)}
        />
      </article>
    )
  }

  const needsNote = !note.trim()
  return (
    <article aria-label={`${agent.identity.name} lifecycle`}>
      <Block title="Clone">
        <p className="mb-3 dash-body text-fg-muted">Start a new agent from this definition. Privileges are copied and flagged in the plan.</p>
        <Btn onClick={() => onClone(id)} disabled={retired}>Clone {agent.identity.name}…</Btn>
      </Block>

      {client.writeMode === 'demonstration' ? <Block title="Retire and restore"><p className="dash-body text-fg-muted">Retirement impact reads are not connected yet. You can edit or clone this agent and try a demonstration save.</p></Block> : retired ? (
        <Block title="Restore">
          <p className="mb-3 dash-body text-fg-muted">Retired {agent.identity.retiredAt ? new Date(agent.identity.retiredAt).toISOString().slice(0, 10) : ''}. Restoring brings the definition back <strong>disabled</strong>, without its former teams or assignments.</p>
          <Field label="Reason (required)" htmlFor="lc-note"><AdminInput id="lc-note" value={note} onChange={(e) => setNote(e.target.value)} className={inputCls} placeholder="Why restore" /></Field>
          {error && <p role="alert" className="mt-2 dash-body text-danger">{error}</p>}
          <div className="mt-3"><Btn tone="primary" onClick={make} disabled={busy || needsNote}>Review restore plan</Btn></div>
        </Block>
      ) : (
        <Block title="Retire">
          <div className="mb-3 flex items-center gap-3"><RiskBadge risk="high" /><span className="dash-body text-fg-muted">Retiring is high risk: it needs a reason and a fresh identity confirmation.</span></div>
          <h4 className="mb-1.5 font-mono dash-caption uppercase tracking-widest text-fg-faint">Impact preview</h4>
          {impact ? (
            <>
              <ImpactSummary impact={impact} />
              {impactTotal(impact) === 0 ? <p className="mt-2 dash-body text-fg-muted">Nothing depends on this agent right now.</p> : (
                <ul aria-label="Impact detail" className="mt-3 space-y-1 dash-body">
                  {impact.sessions.count > 0 && <li className="text-warning">{impact.sessions.count} running session{impact.sessions.count === 1 ? '' : 's'} ({impact.sessions.state}) — <Link to="/agents" className="underline">open in fleet</Link></li>}
                  {[...impact.tasks, ...impact.workstreams].map((a) => <li key={a.id} className="text-fg-muted"><span className="font-mono dash-label uppercase tracking-wider text-fg-faint">{a.target.type}</span> {a.target.id}{a.target.label ? <span className="text-fg-faint"> · {a.target.label}</span> : null}</li>)}
                  {impact.teams.map((t) => <li key={t.teamId} className={t.soleCoordinator ? 'text-danger' : 'text-fg-muted'}><span className="font-mono dash-label uppercase tracking-wider text-fg-faint">team</span> {t.teamName} as {t.role}{t.soleCoordinator ? ' — the only coordinator' : ''}</li>)}
                </ul>
              )}
            </>
          ) : <p className="font-mono dash-support text-fg-faint">Loading impact…</p>}
          {impact && impact.sessions.count > 0 && (
            <div className="mt-4 max-w-sm">
              <Field label="Running sessions" htmlFor="lc-sessions">
                <AdminSelect id="lc-sessions" value={sessions} onChange={(e) => setSessions(e.target.value as RetireSessions)} className={inputCls}>
                  <option value="drain">Let them finish (drain)</option>
                  <option value="stop">Stop them now</option>
                </AdminSelect>
              </Field>
            </div>
          )}
          <div className="mt-4"><Field label="Reason (required)" htmlFor="lc-note"><AdminInput id="lc-note" value={note} onChange={(e) => setNote(e.target.value)} className={inputCls} placeholder="Why retire this agent" /></Field></div>
          {error && <p role="alert" className="mt-2 dash-body text-danger">{error}</p>}
          <div className="mt-3"><Btn tone="danger" onClick={make} disabled={busy || needsNote || !impact}>Review retire plan</Btn></div>
        </Block>
      )}
    </article>
  )
}

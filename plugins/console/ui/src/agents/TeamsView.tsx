import { AdminDirectory, AdminRecordButton } from '@hollis-labs/kit-admin'
import { AdminInput, AdminSelect } from '@hollis-labs/kit-admin'
import { useCallback, useEffect, useMemo, useState } from 'react'
import { EmptyState } from '@hollis-labs/design-components'
import { IdentityAvatar } from '@hollis-labs/kit-admin'
import { useAgentsClient } from './context'
import { Field, inputCls, ListField } from './AgentEditor'
import { blankTeamDraft, computeLifecyclePlan, slugify, teamDraftFrom, TEAM_ROLES, type TeamDraft, type TeamRecord } from './lifecycle'
import { KNOWN_RULES, KNOWN_SCOPES, PERMISSION_MODES, type AgentPlan } from './plan'
import { PlanRunner } from './PlanRunner'
import { Block, Btn, Chip, Heading, RiskBadge } from './ui'
import type { AgentRecord } from './types'

const TONE: Record<string, string> = { coordinator: 'border-warning/60 text-warning', reviewer: 'border-info/50 text-info' }

function TeamDetail({ team, agents, onEdit, onDelete }: { team: TeamRecord; agents: AgentRecord[]; onEdit: () => void; onDelete: () => void }) {
  const byId = new Map(agents.map((a) => [a.identity.id, a]))
  return (
    <article aria-label={`${team.name} team`}>
      <header className="flex flex-wrap items-center gap-3 border-b border-border/80 px-5 py-4">
        <div className="min-w-0 flex-1">
          <h2 className="truncate font-mono dash-detail-title font-semibold uppercase tracking-wide">{team.name}</h2>
          <div className="font-mono dash-support text-fg-faint">{team.id} · revision {team.revision}</div>
          {team.summary && <p className="mt-1 dash-body text-fg-muted">{team.summary}</p>}
        </div>
        <Btn onClick={onEdit}>Edit team</Btn>
        <Btn tone="danger" onClick={onDelete}>Delete…</Btn>
      </header>
      <Block title={`Members (${team.members.length})`}>
        {team.members.length === 0 ? <p className="dash-body text-fg-faint">No members.</p> : (
          <ul aria-label="Members" className="divide-y divide-border/80 border border-border/80">
            {team.members.map((m) => {
              const a = byId.get(m.agentId)
              return (
                <li key={m.agentId} className="flex items-center gap-3 px-3 py-2">
                  <IdentityAvatar identity={m.agentId} size="small" />
                  <span className="min-w-0 flex-1"><span className="block truncate font-mono dash-body text-fg">{a?.identity.name ?? m.agentId}</span><span className="block truncate font-mono dash-label text-fg-faint">{m.agentId}{a?.identity.retired ? ' · retired' : !a?.identity.enabled ? ' · disabled' : ''}</span></span>
                  <Chip tone={TONE[m.role]}>{m.role}</Chip>
                </li>
              )
            })}
          </ul>
        )}
      </Block>
      <Block title="Shared policy">
        <dl className="space-y-1.5 dash-body">
          <div className="flex gap-3"><dt className="w-36 shrink-0 font-mono dash-label uppercase tracking-wider text-fg-faint">Permission ceiling</dt><dd className={team.policy.permissionCeiling === 'bypass' ? 'text-warning' : 'text-fg'}>{team.policy.permissionCeiling}</dd></div>
          <div className="flex gap-3"><dt className="w-36 shrink-0 font-mono dash-label uppercase tracking-wider text-fg-faint">Scope ceiling</dt><dd className="flex flex-wrap gap-1.5">{team.policy.scopesCeiling.length ? team.policy.scopesCeiling.map((s) => <Chip key={s} tone="border-warning/60 text-warning">{s}</Chip>) : <span className="text-fg-faint">None</span>}</dd></div>
          <div className="flex gap-3"><dt className="w-36 shrink-0 font-mono dash-label uppercase tracking-wider text-fg-faint">Rules</dt><dd className="flex flex-wrap gap-1.5">{team.policy.rules.length ? team.policy.rules.map((s) => <Chip key={s}>{s}</Chip>) : <span className="text-fg-faint">None</span>}</dd></div>
          <div className="flex gap-3"><dt className="w-36 shrink-0 font-mono dash-label uppercase tracking-wider text-fg-faint">Reports to</dt><dd className="text-fg">{team.policy.reportTo ?? <span className="text-fg-faint">not set</span>}</dd></div>
        </dl>
      </Block>
    </article>
  )
}

function TeamEditor({ team, agents, onCancel, onDone }: { team?: TeamRecord; agents: AgentRecord[]; onCancel: () => void; onDone: (t?: TeamRecord) => void }) {
  const client = useAgentsClient()
  const [draft, setDraft] = useState<TeamDraft>(() => (team ? teamDraftFrom(team) : blankTeamDraft()))
  const [id, setId] = useState('')
  const [idTouched, setIdTouched] = useState(false)
  const [note, setNote] = useState('')
  const [plan, setPlan] = useState<AgentPlan | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [pick, setPick] = useState('')
  const [teams, setTeams] = useState<TeamRecord[]>([])
  useEffect(() => { client.listTeams().then(setTeams, () => undefined) }, [client])

  const teamId = team?.id ?? (idTouched ? id : `tm_${slugify(draft.name).replace(/-/g, '_')}`)
  const req = (team ? { op: 'team.update' as const, teamId, team: draft } : { op: 'team.create' as const, teamId, team: draft })
  const live = useMemo(
    () => computeLifecyclePlan({ ...req, note, baseRevision: team?.revision }, { agents, teams, assignments: [] }, { id: 'preview', now: 0, actor: { id: 'preview' } }),
    [agents, teams, draft, note, teamId, team],
  )
  const errors = live.issues.filter((i) => i.level === 'error' && !(!team && !draft.members.length && false))
  const addable = agents.filter((a) => !a.identity.retired && !draft.members.some((m) => m.agentId === a.identity.id))
  const setPolicy = <K extends keyof TeamDraft['policy']>(k: K, v: TeamDraft['policy'][K]) => setDraft((d) => ({ ...d, policy: { ...d.policy, [k]: v } }))

  const review = async () => {
    setBusy(true)
    setError(null)
    try {
      setPlan(await client.planLifecycle({ ...req, note, baseRevision: team?.revision }))
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Could not plan')
    } finally {
      setBusy(false)
    }
  }

  if (plan) {
    return <PlanRunner plan={plan} subjectLabel={draft.name || teamId} onBack={() => setPlan(null)} onDone={() => onDone()} onChanged={(o) => o.status === 'applied' && onDone(o.team)} />
  }
  return (
    <article aria-label={team ? `Edit ${team.name}` : 'New team'}>
      <header className="flex flex-wrap items-center gap-3 border-b border-border/80 px-5 py-3">
        <h2 className="font-mono dash-body font-semibold uppercase tracking-wide">{team ? `Edit ${team.name}` : 'New team'}</h2>
        <span className="ml-auto flex items-center gap-2"><RiskBadge risk={live.risk} /><Btn onClick={onCancel}>Close</Btn></span>
      </header>
      <Block title="Team">
        <div className="grid gap-3 sm:grid-cols-2">
          <Field label="Name" htmlFor="tm-name"><AdminInput id="tm-name" value={draft.name} onChange={(e) => setDraft({ ...draft, name: e.target.value })} className={inputCls} /></Field>
          <Field label="Id" htmlFor="tm-id"><AdminInput id="tm-id" value={teamId} disabled={!!team} onChange={(e) => { setId(e.target.value); setIdTouched(true) }} className={inputCls} /></Field>
          <div className="sm:col-span-2"><Field label="Summary" htmlFor="tm-summary"><AdminInput id="tm-summary" value={draft.summary} onChange={(e) => setDraft({ ...draft, summary: e.target.value })} className={inputCls} /></Field></div>
        </div>
      </Block>
      <Block title={`Members (${draft.members.length})`}>
        <ul aria-label="Team members" className="mb-3 divide-y divide-border/80 border border-border/80">
          {draft.members.map((m) => (
            <li key={m.agentId} className="flex items-center gap-3 px-3 py-1.5">
              <span className="min-w-0 flex-1 truncate font-mono dash-body text-fg">{agents.find((a) => a.identity.id === m.agentId)?.identity.name ?? m.agentId}</span>
              <AdminSelect aria-label={`Role for ${m.agentId}`} value={m.role} onChange={(e) => setDraft({ ...draft, members: draft.members.map((x) => (x.agentId === m.agentId ? { ...x, role: e.target.value } : x)) })} className="h-8 border border-border bg-bg px-2 font-mono dash-support text-fg">
                {[...new Set([...TEAM_ROLES, m.role])].map((r) => <option key={r}>{r}</option>)}
              </AdminSelect>
              <Btn onClick={() => setDraft({ ...draft, members: draft.members.filter((x) => x.agentId !== m.agentId) })} aria-label={`Remove ${m.agentId}`}>Remove</Btn>
            </li>
          ))}
          {draft.members.length === 0 && <li className="px-3 py-2 dash-body text-fg-faint">No members yet.</li>}
        </ul>
        <div className="flex items-center gap-2">
          <AdminSelect aria-label="Add member" value={pick} onChange={(e) => setPick(e.target.value)} className={`${inputCls} max-w-xs`}>
            <option value="">Add an agent…</option>
            {addable.map((a) => <option key={a.identity.id} value={a.identity.id}>{a.identity.name}</option>)}
          </AdminSelect>
          <Btn disabled={!pick} onClick={() => { setDraft({ ...draft, members: [...draft.members, { agentId: pick, role: draft.members.length ? 'worker' : 'coordinator' }] }); setPick('') }}>Add</Btn>
        </div>
      </Block>
      <Block title="Shared policy">
        <div className="space-y-3">
          <Field label="Permission ceiling" htmlFor="tm-perm"><AdminSelect id="tm-perm" value={draft.policy.permissionCeiling} onChange={(e) => setPolicy('permissionCeiling', e.target.value)} className={`${inputCls} max-w-xs`}>{PERMISSION_MODES.map((p) => <option key={p}>{p}</option>)}</AdminSelect></Field>
          <Field label="Scope ceiling"><ListField label="Scope ceiling" values={draft.policy.scopesCeiling} suggestions={KNOWN_SCOPES} tone="border-warning/60 bg-warning/10 text-warning" onChange={(v) => setPolicy('scopesCeiling', v)} /></Field>
          <Field label="Team rules"><ListField label="Team rules" values={draft.policy.rules} suggestions={KNOWN_RULES} onChange={(v) => setPolicy('rules', v)} /></Field>
          <Field label="Reports to" htmlFor="tm-report">
            <AdminSelect id="tm-report" value={draft.policy.reportTo ?? ''} onChange={(e) => setPolicy('reportTo', e.target.value || undefined)} className={`${inputCls} max-w-xs`}>
              <option value="">not set</option>
              {agents.filter((a) => !a.identity.retired).map((a) => <option key={a.identity.id} value={a.identity.id}>{a.identity.name}</option>)}
            </AdminSelect>
          </Field>
        </div>
      </Block>
      <Block title="Reason">
        <Field label={live.risk === 'high' ? 'Reason (required for high risk)' : 'Reason (optional)'} htmlFor="tm-note" issues={live.issues.filter((i) => i.field === 'note')}><AdminInput id="tm-note" value={note} onChange={(e) => setNote(e.target.value)} className={inputCls} /></Field>
        {live.issues.filter((i) => i.field !== 'note').map((i, n) => <p key={n} className={`mt-1.5 dash-support ${i.level === 'error' ? 'text-danger' : 'text-warning'}`}>{i.message}</p>)}
      </Block>
      {error && <div role="alert" className="border-b border-danger/30 bg-danger/5 px-5 py-2 dash-body text-danger">{error}</div>}
      <div className="sticky bottom-0 flex items-center gap-3 border-t border-border bg-bg px-5 py-3">
        <span className="font-mono dash-label uppercase tracking-widest text-fg-faint">{live.lifecycle?.items.length ?? 0} change{live.lifecycle?.items.length === 1 ? '' : 's'}</span>
        <Btn className="ml-auto" tone="primary" onClick={review} disabled={busy || errors.length > 0}>Review plan</Btn>
      </div>
    </article>
  )
}

type Pane = { kind: 'view' } | { kind: 'edit' } | { kind: 'new' } | { kind: 'delete' }

/** Teams: members, roles and shared policy. Every change is a lifecycle plan with a step-up. */
export function TeamsView({ agents, onAgentsChanged }: { agents: AgentRecord[]; onAgentsChanged?: () => void }) {
  const client = useAgentsClient()
  const [teams, setTeams] = useState<TeamRecord[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [selectedId, setSelectedId] = useState<string | null>(null)
  const [pane, setPane] = useState<Pane>({ kind: 'view' })
  const [note, setNote] = useState('')
  const [plan, setPlan] = useState<AgentPlan | null>(null)

  const load = useCallback(() => client.listTeams().then((t) => (setTeams(t), setError(null)), (e) => setError(e instanceof Error ? e.message : 'Teams unavailable')), [client])
  useEffect(() => { void load() }, [load])

  if (teams === null) return error ? <div className="p-4"><EmptyState variant="error" title="Teams unavailable" description={error} /></div> : <div className="p-4 font-mono dash-support text-fg-faint">Loading teams…</div>
  const selected = teams.find((t) => t.id === selectedId) ?? teams[0] ?? null
  const done = (t?: TeamRecord) => { void load(); onAgentsChanged?.(); if (t) setSelectedId(t.id); setPane({ kind: 'view' }); setPlan(null); setNote('') }

  const planDelete = async () => {
    if (!selected) return
    try { setPlan(await client.planLifecycle({ op: 'team.delete', teamId: selected.id, note, baseRevision: selected.revision })) } catch (e) { setError(e instanceof Error ? e.message : 'Could not plan') }
  }

  return (
    <div>
      <div className="flex flex-wrap items-center gap-x-6 gap-y-1 border-b border-border/80 px-4 py-2 font-mono dash-label uppercase tracking-widest text-fg-faint">
        <span>{teams.length} teams</span>
        <span>{teams.reduce((n, t) => n + t.members.length, 0)} memberships</span>
        <Btn className="ml-auto" tone="primary" onClick={() => setPane({ kind: 'new' })}>New team</Btn>
      </div>
      <AdminDirectory label="Teams" layout="compact" records={
        <ul aria-label="Teams">
          {teams.map((t) => (
            <li key={t.id}>
              <AdminRecordButton selected={t.id === selected?.id} onSelect={() => { setSelectedId(t.id); setPane({ kind: 'view' }); setPlan(null) }} size="comfortable" title={t.name} description={t.id} badge={`${t.members.length} members`} />
            </li>
          ))}
          {teams.length === 0 && <li className="p-4"><EmptyState variant="empty" title="No teams" description="Create a team to share a policy across agents." /></li>}
        </ul>} detail={<div>
          {pane.kind === 'new' && <TeamEditor key="new" agents={agents} onCancel={() => setPane({ kind: 'view' })} onDone={done} />}
          {pane.kind === 'edit' && selected && <TeamEditor key={selected.id} team={selected} agents={agents} onCancel={() => setPane({ kind: 'view' })} onDone={done} />}
          {pane.kind === 'delete' && selected && (plan ? (
            <PlanRunner plan={plan} subjectLabel={selected.name} onBack={() => setPlan(null)} onDone={() => done()} onChanged={() => { void load(); onAgentsChanged?.() }} />
          ) : (
            <Block title={`Delete ${selected.name}`}>
              <p className="mb-3 dash-body text-fg-muted">All {selected.members.length} members lose this membership and the shared policy stops applying. High risk: a reason is required.</p>
              <Field label="Reason (required)" htmlFor="tm-del-note"><AdminInput id="tm-del-note" value={note} onChange={(e) => setNote(e.target.value)} className={inputCls} /></Field>
              {error && <p role="alert" className="mt-2 dash-body text-danger">{error}</p>}
              <div className="mt-3 flex gap-2"><Btn onClick={() => setPane({ kind: 'view' })}>Cancel</Btn><Btn tone="danger" disabled={!note.trim()} onClick={planDelete}>Review delete plan</Btn></div>
            </Block>
          ))}
          {pane.kind === 'view' && (selected ? <TeamDetail team={selected} agents={agents} onEdit={() => setPane({ kind: 'edit' })} onDelete={() => { setNote(''); setPlan(null); setPane({ kind: 'delete' }) }} /> : <div className="p-4"><Heading>Teams</Heading><p className="dash-body text-fg-faint">Select or create a team.</p></div>)}
        </div>} />
    </div>
  )
}

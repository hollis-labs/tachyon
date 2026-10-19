import { AdminSelect, AdminTextarea } from '@hollis-labs/kit-admin'
import { useEffect, useState } from 'react'
import { Field } from './AgentEditor'
import { isRetired, ownedBuilderClient, type ObjectRecord } from './owned-builder-client'
import { providerActionsClient as client, type ActionReceipt, type ActionSummary, type RosterSnapshot } from './provider-actions'
import { ProviderActionReview } from './ProviderActionReview'
import { useOwnedRecords } from './use-owned-records'
import { Block, Btn } from './ui'

const operations = { assign: 'Dispatch saved assignment', address: 'Message team address', member_add: 'Add member to slot', member_remove: 'Remove member', cancel: 'Cancel member work', dissolve: 'Dissolve run' }
type Operation = keyof typeof operations

function RunOperations({ team, formation, onLocked }: { team: ObjectRecord; formation: ActionReceipt; onLocked(locked: boolean): void }) {
  const assignments = useOwnedRecords(ownedBuilderClient, 'assignment')
  const [assignmentID, setAssignmentID] = useState(''), [operation, setOperation] = useState<Operation>('address')
  const [address, setAddress] = useState(''), [body, setBody] = useState(''), [member, setMember] = useState(''), [slot, setSlot] = useState('')
  const [cascade, setCascade] = useState(false), [roster, setRoster] = useState<RosterSnapshot>()
  const [refreshing, setRefreshing] = useState(false), [problem, setProblem] = useState(''), [reload, setReload] = useState(0)
  useEffect(() => {
    let active = true
    setRefreshing(true)
    void (async () => {
      try {
        const saved = await client.roster(formation.id)
        if (!active) return
        setRoster(saved)
        const fresh = await client.roster(formation.id, true)
        if (active) { setRoster(fresh); setProblem('') }
      } catch (e) { if (active) setProblem(e instanceof Error ? e.message : 'Roster unavailable.') }
      finally { if (active) setRefreshing(false) }
    })()
    return () => { active = false }
  }, [formation.id, reload])
  const members = roster?.runs[0]?.members ?? []
  const slots = (formation.payload.team as { slots?: { name: string }[] } | undefined)?.slots ?? []
  const choices = assignments.rows.filter(row => row.ownership === 'tachyon' && !isRetired(row) && row.definition.team_id === team.id && !['completed', 'cancelled'].includes(String(row.definition.state)))
  const object = operation === 'assign' ? choices.find(row => row.id === assignmentID) : team
  const needsAddress = operation === 'assign' || operation === 'address', needsMember = operation === 'cancel' || operation === 'member_remove'
  const cleanup = operation === 'cancel' || operation === 'member_remove' || operation === 'dissolve'
  const invalid = !object || isRetired(team) && !cleanup || needsAddress && (!address || !body.trim()) || needsMember && !member || operation === 'member_add' && !slot
  return <>
    <Block title="Run membership">
      <p className="dash-support text-fg-muted">{roster?.observed_at ? `Saved roster observed ${new Date(roster.observed_at).toLocaleString()}` : 'No saved roster yet.'} {refreshing && 'Refreshing in the background…'}</p>
      <p className="dash-body">{roster?.runs[0]?.run.status ?? 'Runtime status unknown'}</p>
      {(problem || roster?.problem) && <p role="alert" className="dash-body text-warning">{problem || roster?.problem}</p>}
      <ul className="space-y-1 dash-support">{members.map(m => <li key={m.id}>{m.slot} · {m.id} · {m.status} · {m.actor}</li>)}</ul>
      <Btn disabled={refreshing} onClick={() => setReload(v => v + 1)}>Refresh roster</Btn>
    </Block>
    <ProviderActionReview objectID={object?.id ?? team.id} disabled={!!invalid} onLocked={onLocked} preview={id => client.runtime({
      id, object_id: object!.id, expected_revision: object!.revision, formation_id: formation.id, operation,
      ...(needsAddress ? { address, body } : {}), ...(needsMember ? { member_id: member } : {}),
      ...(operation === 'member_add' ? { slot } : {}), ...(operation === 'cancel' ? { cascade } : {}),
    })}>
      <Field label="Team operation" htmlFor="runtime-operation"><AdminSelect id="runtime-operation" value={operation} onChange={e => { setOperation(e.target.value as Operation); setAddress('') }}>{Object.entries(operations).map(([value, label]) => <option key={value} value={value}>{label}</option>)}</AdminSelect></Field>
      {operation === 'assign' && <Field label="Saved assignment" htmlFor="runtime-assignment"><AdminSelect id="runtime-assignment" value={assignmentID} onChange={e => setAssignmentID(e.target.value)}><option value="">Choose assignment…</option>{choices.map(row => <option key={row.id} value={row.id}>{row.name} · revision {row.revision}</option>)}</AdminSelect>
        {assignments.error && <p role="alert">{assignments.error}</p>}{assignments.next && <Btn disabled={assignments.loading} onClick={() => void assignments.more()}>More assignment choices</Btn>}
        <p className="dash-support text-fg-muted">Dispatch sends the saved assignment and your briefing. Its planning status changes only when you edit it.</p>
      </Field>}
      {needsAddress && <>
        <Field label="Team address" htmlFor="runtime-address"><AdminSelect id="runtime-address" value={address} onChange={e => setAddress(e.target.value)}><option value="">Choose address…</option>{operation === 'address' && <option value="@all">Whole team</option>}{slots.map(s => <option key={s.name} value={'@' + s.name}>Slot: {s.name}</option>)}</AdminSelect></Field>
        <Field label="Message or assignment briefing" htmlFor="runtime-body"><AdminTextarea id="runtime-body" value={body} onChange={e => setBody(e.target.value)} rows={5} maxLength={48000} /></Field>
      </>}
      {operation === 'member_add' && <Field label="Formation slot" htmlFor="runtime-slot"><AdminSelect id="runtime-slot" value={slot} onChange={e => setSlot(e.target.value)}><option value="">Choose slot…</option>{slots.map(s => <option key={s.name} value={s.name}>{s.name}</option>)}</AdminSelect><p className="dash-support text-fg-muted">Adding a member may launch an agent process.</p></Field>}
      {needsMember && <Field label="Run member" htmlFor="runtime-member"><AdminSelect id="runtime-member" value={member} onChange={e => setMember(e.target.value)}><option value="">Choose member…</option>{member && !members.some(m => m.id === member) && <option value={member}>{member} · no longer in saved roster</option>}{members.map(m => <option key={m.id} value={m.id}>{m.slot} · {m.id} · {m.status}</option>)}</AdminSelect></Field>}
      {operation === 'cancel' && <label className="flex items-center gap-2 dash-body"><input type="checkbox" checked={cascade} onChange={e => setCascade(e.target.checked)} />Also cancel delegated children</label>}
      {operation === 'dissolve' && <p className="dash-body text-warning">Dissolving ends this run and its active membership. Its saved team definition is retained.</p>}
    </ProviderActionReview>
  </>
}

export function TeamOperations() {
  const teams = useOwnedRecords(ownedBuilderClient, 'team')
  const [team, setTeam] = useState<ObjectRecord>(), [formation, setFormation] = useState<ActionReceipt>()
  const [history, setHistory] = useState<ActionSummary[]>([]), [next, setNext] = useState<string>()
  const [locked, setLocked] = useState(false), [busy, setBusy] = useState(false), [problem, setProblem] = useState('')
  async function load(id: string, cursor = '') {
    setBusy(true); setProblem('')
    try {
      const row = await ownedBuilderClient.get(id), page = await client.list(id, cursor, 'teams.form')
      setTeam(row); setHistory(old => cursor ? [...old, ...page.items] : page.items); setNext(page.next_before_id)
    } catch (e) { setProblem(e instanceof Error ? e.message : 'Formation history unavailable.') }
    finally { setBusy(false) }
  }
  async function select(id: string) {
    setBusy(true); setProblem('')
    try { setFormation(await client.get(id)) }
    catch (e) { setProblem(e instanceof Error ? e.message : 'Formation unavailable.') }
    finally { setBusy(false) }
  }
  return <section aria-label="Team operations">
    <Block title="Team operations">
      <p className="dash-body text-fg-muted">Choose a Tachyon-owned team and a confirmed formation. Run selection is explicit; imported and externally formed runs need a provider mapping before they can be managed here.</p>
      <Field label="Saved team" htmlFor="runtime-team"><AdminSelect id="runtime-team" disabled={busy || locked} value={team?.id ?? ''} onChange={e => { setFormation(undefined); setTeam(undefined); setHistory([]); setNext(undefined); if (e.target.value) void load(e.target.value) }}><option value="">Choose team…</option>{teams.rows.filter(row => row.ownership === 'tachyon').map(row => <option key={row.id} value={row.id}>{row.name}{isRetired(row) ? ' · retired' : ''}</option>)}</AdminSelect></Field>
      {teams.next && <Btn disabled={teams.loading} onClick={() => void teams.more()}>More team choices</Btn>}
      {(problem || teams.error) && <p role="alert" className="dash-body text-warning">{problem || teams.error}</p>}
      {team && <>
        <Field label="Confirmed formation" htmlFor="runtime-formation"><AdminSelect id="runtime-formation" disabled={busy || locked} value={formation?.id ?? ''} onChange={e => { setFormation(undefined); if (e.target.value) void select(e.target.value) }}><option value="">Choose confirmed formation…</option>{history.filter(row => row.state === 'succeeded').map(row => <option key={row.id} value={row.id}>{new Date(row.created_at).toLocaleString()} · {row.id}</option>)}</AdminSelect></Field>
        {next && <Btn disabled={busy || locked} onClick={() => void load(team.id, next)}>More formation receipts</Btn>}
        <Btn disabled={busy || locked} onClick={() => { setFormation(undefined); void load(team.id) }}>Refresh formation choices</Btn>
      </>}
    </Block>
    {team && formation && <RunOperations key={formation.id} team={team} formation={formation} onLocked={setLocked} />}
  </section>
}

import { AdminDirectory, AdminInput, AdminRecordButton, AdminSelect, AdminTextarea } from '@hollis-labs/kit-admin'
import { useEffect, useRef, useState } from 'react'
import { Field } from './AgentEditor'
import { Block, Btn } from './ui'
import { builderDraft, builderIssues, builderPatch, type BuilderDraft } from './owned-builder-draft'
import { isRetired, newObjectID, ownedBuilderClient, type BuilderKind, type HistoryPage, type ObjectRecord, type OwnedBuilderClient } from './owned-builder-client'
import { useOwnedRecords } from './use-owned-records'
import { AgentPublication } from './AgentPublication'

function ReferencePicker({ client, kind, value, onChange, label }: { client: OwnedBuilderClient; kind: string; value: string; onChange(value: string): void; label: string }) {
  const records = useOwnedRecords(client, kind)
  const [current, setCurrent] = useState<ObjectRecord>()
  useEffect(() => {
    let live = true
    setCurrent(undefined)
    if (value) void client.get(value).then(row => { if (live && row.kind === kind) setCurrent(row) }, () => undefined)
    return () => { live = false }
  }, [client, kind, value])
  return <Field label={label} htmlFor={`ref-${label}`}><AdminSelect id={`ref-${label}`} value={value} onChange={e => onChange(e.target.value)}>
    <option value="">Choose {kind}…</option>
    {value && !records.rows.some(row => row.id === value) && <option value={value}>{current?.name ?? value}</option>}
    {records.rows.map(row => <option key={row.id} value={row.id} disabled={isRetired(row)}>{row.name}{isRetired(row) ? ' · retired' : ''}</option>)}
  </AdminSelect>{records.error && <span role="alert">{records.error}</span>}{records.next && <Btn disabled={records.loading} onClick={() => void records.more()}>More {kind} choices</Btn>}</Field>
}

const labels: Record<string, string> = { name: 'Name', description: 'Description', runtime: 'Runtime', model: 'Model', roles: 'Roles (comma separated)', instructions: 'Instructions', tools: 'Tools (comma separated)', skills: 'Skills (comma separated)', enabled: 'Enabled in desired configuration', project_id: 'Project', machine_id: 'Machine', role: 'Assignment role', state: 'Planning status', agent_id: 'Agent', team_id: 'Team', target: 'Local target', external_target: 'External target', provider: 'Provider', instance: 'Instance', collection: 'Collection', external_id: 'External ID' }

export function OwnedBuilderView({ kind, client = ownedBuilderClient }: { kind: BuilderKind; client?: OwnedBuilderClient }) {
  const records = useOwnedRecords(client, kind)
  const [selected, setSelected] = useState<string>()
  const [base, setBase] = useState<ObjectRecord>()
  const [draft, setDraft] = useState<BuilderDraft>()
  const [review, setReview] = useState(false)
  const [busy, setBusy] = useState(false)
  const [problem, setProblem] = useState(''), [message, setMessage] = useState('')
  const [history, setHistory] = useState<HistoryPage>()
  const [filter, setFilter] = useState('')
  const pending = useRef(false)
  const dirty = !!draft && (!base || Object.keys(builderPatch(kind, draft, base)).length > 0)
  const issues = draft ? builderIssues(kind, draft) : []
  const locked = !!base && (base.ownership !== 'tachyon' || isRetired(base))
  const newer = base && records.rows.find(row => row.id === base.id && row.revision > base.revision)
  function install(row: ObjectRecord) {
    setSelected(row.id); setBase(row); setDraft(builderDraft(kind, row)); setReview(false); setHistory(undefined)
  }
  async function run(action: () => Promise<void>) {
    if (pending.current) return
    pending.current = true; setBusy(true); setProblem(''); setMessage('')
    try { await action() } catch (e) { setProblem(e instanceof Error ? e.message : 'The operation failed. Your draft is preserved.') }
    finally { pending.current = false; setBusy(false) }
  }
  const open = (id: string) => run(async () => {
    const row = await client.get(id)
    if (row.kind !== kind) throw new Error('Unexpected object kind.')
    install(row)
  })
  const create = () => { setSelected(newObjectID()); setBase(undefined); setDraft(builderDraft(kind)); setReview(false); setHistory(undefined); setProblem(''); setMessage('') }
  const save = () => run(async () => {
    if (!draft || !selected || issues.length || !review || locked) return
    const row = await client.save(kind, selected, builderPatch(kind, draft, base), base)
    records.commit(row); install(row); setMessage('Saved in Tachyon.')
  })
  const checkCreate = () => run(async () => {
    if (!selected || !draft || base) return
    const row = await client.get(selected)
    if (row.kind !== kind) throw new Error('Unexpected object kind.')
    records.commit(row); setBase(row); setReview(false)
    setMessage('The create was saved. Your current draft is preserved for comparison and further edits.')
  })
  const lifecycle = (action: 'delete' | 'restore' | 'take_ownership') => run(async () => {
    if (!base) return
    const row = await client.lifecycle(base, action)
    records.commit(row); install(row)
    setMessage(action === 'take_ownership' ? 'Tachyon now owns this definition. Provider provenance is retained.' : action === 'delete' ? 'Retired. History is retained.' : 'Restored.')
  })
  const readHistory = () => run(async () => {
    if (!base) return
    const page = await client.history(base.id, history?.next_after_revision ?? 0)
    setHistory(old => ({ ...page, items: [...(old?.items ?? []), ...page.items] }))
  })
  const set = (key: string, value: string | boolean) => { setDraft(d => d && ({ ...d, [key]: value })); setReview(false); setMessage('') }
  const input = (key: string, multiline = false) => <Field key={key} label={labels[key] ?? key} htmlFor={`owned-${key}`}>{multiline
    ? <AdminTextarea id={`owned-${key}`} value={String(draft?.[key] ?? '')} onChange={e => set(key, e.target.value)} rows={key === 'instructions' ? 7 : 3} />
    : <AdminInput id={`owned-${key}`} value={String(draft?.[key] ?? '')} onChange={e => set(key, e.target.value)} />}</Field>
  const picker = (key: string, objectKind: string, label: string) => <ReferencePicker key={`${key}-${objectKind}`} client={client} kind={objectKind} label={label} value={String(draft?.[key] ?? '')} onChange={v => set(key, v)} />

  return <div>
    <div className="flex flex-wrap items-center gap-3 border-b border-border px-4 py-3">
      <p className="flex-1 dash-body text-fg-muted">{kind === 'agent' ? 'Agent definitions owned by Tachyon. Saving or enabling a definition does not start or change a running agent.' : 'Planned work for an agent or team. Saving an assignment does not dispatch it.'}</p>
      <Btn onClick={create} disabled={busy || dirty}>New {kind}</Btn><Btn disabled={busy || records.loading} onClick={() => void records.refresh()}>Refresh</Btn>
    </div>
    {records.error && <p role="alert" className="px-4 py-2 dash-body text-danger">{records.error} Showing the last loaded records.</p>}
    {problem && <p role="alert" className="px-4 py-2 dash-body text-danger">{problem}</p>}
    {message && <p role="status" className="px-4 py-2 dash-body text-success">{message}</p>}
    <AdminDirectory label={`${kind} definitions`} layout="compact" records={<aside>
      <div className="px-4 py-3"><AdminInput aria-label="Filter loaded records" placeholder="Filter loaded records" value={filter} onChange={e => setFilter(e.target.value)} /></div>
      <ul aria-label={`${kind} records`}>{records.rows.filter(row => row.name.toLowerCase().includes(filter.toLowerCase())).map(row => <li key={row.id}><AdminRecordButton selected={selected === row.id} onSelect={() => { if (!busy && !dirty) void open(row.id) }} title={row.name} description={`Revision ${row.revision} · ${isRetired(row) ? 'retired' : row.ownership === 'tachyon' ? 'owned' : 'provider observation'}`} /></li>)}</ul>
      {records.loading && <p role="status" className="px-4 py-2 dash-support">Refreshing…</p>}
      {records.next && <Btn disabled={records.loading} onClick={() => void records.more()}>Load more {kind}s</Btn>}
    </aside>} detail={draft ? <article aria-label={`${kind} editor`}>
      <Block title={base ? `${base.name} · revision ${base.revision}` : `New ${kind}`}>
        {base?.source && <p className="dash-support text-fg-muted">Source: {base.source.key.provider} · {base.source.key.external_id}</p>}
        {newer && <p role="status" className="dash-body text-warning">A newer revision is available. Your draft is preserved.</p>}
        {dirty && <p className="dash-support text-fg-muted">Save or discard your edits before selecting another record.</p>}
        {locked && <p className="dash-body text-fg-muted">{isRetired(base!) ? 'This record is retired.' : 'This provider observation is read-only. Adopt it to manage its definition in Tachyon.'}</p>}
      </Block>
      {review ? <Block title="Review changes"><dl className="space-y-3">{Object.entries(builderPatch(kind, draft, base)).map(([key, value]) => <div key={key}><dt className="dash-label text-fg-muted">{labels[key] ?? key}</dt><dd className="whitespace-pre-wrap break-words dash-body">{value === null ? 'Clear' : typeof value === 'string' ? value || 'Empty' : JSON.stringify(value)}</dd></div>)}</dl><div className="mt-4 flex gap-3"><Btn disabled={busy} onClick={() => setReview(false)}>Continue editing</Btn><Btn tone="primary" disabled={busy || !!issues.length} onClick={() => void save()}>Save {kind}</Btn></div></Block>
      : <fieldset disabled={busy || locked} className="min-w-0"><Block title="Definition"><div className="grid gap-4 sm:grid-cols-2">
        {input('name')}{input('description', true)}
        {kind === 'agent' ? <>
          {input('runtime')}{input('model')}{input('roles')}{input('tools')}{input('skills')}
          <label className="flex items-center gap-2 dash-body"><input type="checkbox" checked={draft.enabled === true} onChange={e => set('enabled', e.target.checked)} />Enabled in desired configuration</label>
          {picker('project_id', 'project', 'Project')}{picker('machine_id', 'machine', 'Machine')}
          <div className="sm:col-span-2">{input('instructions', true)}</div>
        </> : <>
          <Field label="Assignee type" htmlFor="assignee-type"><AdminSelect id="assignee-type" value={String(draft.assignee_kind)} onChange={e => { set('assignee_kind', e.target.value); set('assignee_id', '') }}><option value="agent">Agent</option><option value="team">Team</option></AdminSelect></Field>
          {picker('assignee_id', String(draft.assignee_kind), 'Assignee')}
          <Field label="Target source" htmlFor="target-source"><AdminSelect id="target-source" value={String(draft.target_mode)} onChange={e => set('target_mode', e.target.value)}><option value="local">Tachyon record</option><option value="external">External reference</option></AdminSelect></Field>
          {draft.target_mode === 'local' ? <><Field label="Work type" htmlFor="work-type"><AdminSelect id="work-type" value={String(draft.target_kind)} onChange={e => { set('target_kind', e.target.value); set('target_id', '') }}>{[...new Set(['task', 'plan', 'workstream', 'project', 'epic', 'sprint', String(draft.target_kind)])].map(k => <option key={k} value={k}>{k}</option>)}</AdminSelect></Field>{picker('target_id', String(draft.target_kind), 'Assigned work')}</>
            : <>{['provider', 'instance', 'collection', 'external_id'].map(key => input(key))}<p className="dash-support text-fg-muted">External references are retained as entered; they are not treated as verified local records.</p></>}
          {input('role')}<Field label="Planning status" htmlFor="assignment-state"><AdminSelect id="assignment-state" value={String(draft.state)} onChange={e => set('state', e.target.value)}>{['draft', 'active', 'completed', 'cancelled'].map(v => <option key={v} value={v}>{v}</option>)}</AdminSelect></Field>
        </>}
      </div></Block><div className="flex gap-3 px-4 py-3"><Btn tone="primary" disabled={!dirty || !!issues.length} onClick={() => setReview(true)}>Review {kind}</Btn>{issues.length > 0 && <p className="dash-support text-fg-muted">{issues.join(' ')}</p>}</div></fieldset>}
      <div className="flex flex-wrap gap-3 px-4 py-3">
        <Btn disabled={busy} onClick={() => base ? void open(base.id) : (setDraft(undefined), setSelected(undefined), setReview(false))}>{base ? 'Discard draft and reload saved version' : 'Discard draft'}</Btn>
        {!base && selected && problem && <Btn disabled={busy} onClick={() => void checkCreate()}>Check whether create was saved</Btn>}
        {base && !dirty && (base.ownership === 'tachyon' ? <Btn disabled={busy} onClick={() => void lifecycle(base.deleted_at ? 'restore' : 'delete')}>{base.deleted_at ? 'Restore' : 'Retire'} {kind}</Btn> : !isRetired(base) && <Btn disabled={busy} onClick={() => void lifecycle('take_ownership')}>Adopt definition into Tachyon</Btn>)}
        {base && !history && <Btn disabled={busy} onClick={() => void readHistory()}>Load history</Btn>}
      </div>
      {kind === 'agent' && base?.ownership === 'tachyon' && <AgentPublication key={base.id} agent={base} disabled={dirty || busy || isRetired(base)} />}
      {history && <Block title="Saved history"><ul>{history.items.map(item => <li key={item.record.revision} className="py-2 dash-support">Revision {item.record.revision} · {item.operation} · {item.actor} · {item.recorded_at}</li>)}</ul>{history.next_after_revision && <Btn disabled={busy} onClick={() => void readHistory()}>More history</Btn>}</Block>}
    </article> : <div className="p-6 dash-body text-fg-muted">Select a saved {kind} or create one.</div>} />
  </div>
}

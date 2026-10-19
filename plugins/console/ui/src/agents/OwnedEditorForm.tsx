import { AdminInput, AdminSelect, AdminTextarea } from '@hollis-labs/kit-admin'
import { useEffect, useState } from 'react'
import { Field } from './AgentEditor'
import { Block, Btn } from './ui'
import { builderPatch } from './owned-builder-draft'
import { isRetired, type ObjectRecord, type OwnedBuilderClient } from './owned-builder-client'
import { useOwnedRecords } from './use-owned-records'
import type { OwnedEditor } from './use-owned-editor'

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

export const OWNED_LABELS: Record<string, string> = { name: 'Name', description: 'Description', runtime: 'Runtime', model: 'Model', roles: 'Roles (comma separated)', instructions: 'Instructions', tools: 'Tools (comma separated)', skills: 'Skills (comma separated)', enabled: 'Enabled in desired configuration', project_id: 'Project', machine_id: 'Machine', role: 'Assignment role', state: 'Planning status', agent_id: 'Agent', team_id: 'Team', target: 'Local target', external_target: 'External target', provider: 'Provider', instance: 'Instance', collection: 'Collection', external_id: 'External ID' }

/** Edit form, change review and save for one owned record. Lifecycle and history live in their own tabs. */
export function OwnedEditorForm({ editor }: { editor: OwnedEditor }) {
  const { kind, client, draft, base, review, setReview, busy, issues, dirty, locked, selected, problem, set, save, checkCreate, discard } = editor
  if (!draft) return null
  const input = (key: string, multiline = false) => <Field key={key} label={OWNED_LABELS[key] ?? key} htmlFor={`owned-${key}`}>{multiline
    ? <AdminTextarea id={`owned-${key}`} value={String(draft[key] ?? '')} onChange={e => set(key, e.target.value)} rows={key === 'instructions' ? 7 : 3} />
    : <AdminInput id={`owned-${key}`} value={String(draft[key] ?? '')} onChange={e => set(key, e.target.value)} />}</Field>
  const picker = (key: string, objectKind: string, label: string) => <ReferencePicker key={`${key}-${objectKind}`} client={client} kind={objectKind} label={label} value={String(draft[key] ?? '')} onChange={v => set(key, v)} />
  return <article aria-label={`${kind} editor`}>
    {review ? <Block title="Review changes"><dl className="space-y-3">{Object.entries(builderPatch(kind, draft, base)).map(([key, value]) => <div key={key}><dt className="dash-label text-fg-muted">{OWNED_LABELS[key] ?? key}</dt><dd className="whitespace-pre-wrap break-words dash-body">{value === null ? 'Clear' : typeof value === 'string' ? value || 'Empty' : JSON.stringify(value)}</dd></div>)}</dl><div className="mt-4 flex gap-3"><Btn disabled={busy} onClick={() => setReview(false)}>Continue editing</Btn><Btn tone="primary" disabled={busy || !!issues.length} onClick={() => void save()}>Save {kind}</Btn></div></Block>
    : <fieldset disabled={busy || locked} className="min-w-0"><Block title={base ? 'Definition' : `New ${kind}`}><div className="grid gap-4 sm:grid-cols-2">
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
          : <>{['provider', 'instance', 'collection', 'external_id'].map(key => input(key))}<p className="dash-support text-fg-muted sm:col-span-2">External references are retained as entered; they are not treated as verified local records.</p></>}
        {input('role')}<Field label="Planning status" htmlFor="assignment-state"><AdminSelect id="assignment-state" value={String(draft.state)} onChange={e => set('state', e.target.value)}>{['draft', 'active', 'completed', 'cancelled'].map(v => <option key={v} value={v}>{v}</option>)}</AdminSelect></Field>
      </>}
    </div></Block><div className="flex gap-3 px-4 py-3"><Btn tone="primary" disabled={!dirty || !!issues.length} onClick={() => setReview(true)}>Review {kind}</Btn>{issues.length > 0 && <p className="dash-support text-fg-muted">{issues.join(' ')}</p>}</div></fieldset>}
    <div className="flex flex-wrap gap-3 px-4 py-3">
      <Btn disabled={busy} onClick={discard}>{base ? 'Discard draft and reload saved version' : 'Discard draft'}</Btn>
      {!base && selected && problem && <Btn disabled={busy} onClick={() => void checkCreate()}>Check whether create was saved</Btn>}
    </div>
  </article>
}

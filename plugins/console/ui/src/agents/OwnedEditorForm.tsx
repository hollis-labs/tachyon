import { AdminInput, AdminSelect, AdminTextarea } from '@hollis-labs/kit-admin'
import { useEffect, useState, type ReactNode } from 'react'
import { Field, ListField } from './AgentEditor'
import { KNOWN_RULES, KNOWN_SCOPES, KNOWN_TOOLS, PERMISSION_MODES, PROVIDERS, SANDBOX_MODES } from './plan'
import { Chip } from './ui'
import { Block, Btn } from './ui'
import { builderDraft, builderPatch } from './owned-builder-draft'
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

const ACCENT = 'border-warning/60 bg-warning/10 text-warning'

/** Design-only: the owned agent record has no permission or policy fields yet, so these controls are shown locked and nothing here is saved. */
function PolicyPreview() {
  return <Block title="Permissions and policy"><fieldset disabled className="min-w-0 space-y-3">
    <div className="flex items-center gap-2"><Chip tone="border-border text-fg-faint">not saved yet</Chip><span className="dash-support text-fg-faint">Permissions and policy are not part of the owned agent record yet.</span></div>
    <div className="grid gap-3 sm:grid-cols-2">
      <Field label="Permission mode" htmlFor="owned-perm"><AdminSelect id="owned-perm" className="w-full" value="" onChange={() => undefined}>{['', ...PERMISSION_MODES].map(p => <option key={p} value={p}>{p || 'From launch profile'}</option>)}</AdminSelect></Field>
      <Field label="Sandbox" htmlFor="owned-sandbox"><AdminSelect id="owned-sandbox" className="w-full" value="" onChange={() => undefined}>{['', ...SANDBOX_MODES].map(p => <option key={p} value={p}>{p || 'not set'}</option>)}</AdminSelect></Field>
    </div>
    <Field label="Network" htmlFor="owned-network"><label className="flex items-center gap-2 dash-body text-fg"><input id="owned-network" type="checkbox" checked={false} onChange={() => undefined} /> Allow network access</label></Field>
    <Field label="Privileged scopes"><ListField label="Scopes" values={[]} suggestions={KNOWN_SCOPES} tone={ACCENT} onChange={() => undefined} /></Field>
    <Field label="Policy rules"><ListField label="Rules" values={[]} suggestions={KNOWN_RULES} tone={ACCENT} onChange={() => undefined} /></Field>
  </fieldset></Block>
}

export const OWNED_LABELS: Record<string, string> = { name: 'Name', description: 'Description', runtime: 'Runtime', model: 'Model', roles: 'Roles (comma separated)', instructions: 'Instructions', tools: 'Tools (comma separated)', skills: 'Skills (comma separated)', enabled: 'Enabled in desired configuration', project_id: 'Project', machine_id: 'Machine', role: 'Assignment role', state: 'Planning status', agent_id: 'Agent', team_id: 'Team', target: 'Local target', external_target: 'External target', provider: 'Provider', instance: 'Instance', collection: 'Collection', external_id: 'External ID' }

/** Edit form, change review and save for one owned record. Lifecycle and history live in their own tabs. */
export function OwnedEditorForm({ editor, avatar }: { editor: OwnedEditor; avatar?: ReactNode }) {
  const { kind, client, draft, base, review, setReview, busy, issues, dirty, locked, selected, problem, set, save, checkCreate, discard } = editor
  if (!draft) return null
  const orig = base ? builderDraft(kind, base) : undefined
  const changed = (key: string) => !!orig && String(orig[key]) !== String(draft[key])
  const changes = orig ? Object.keys(draft).filter(changed).length : 0
  const input = (key: string, multiline = false) => <Field key={key} label={OWNED_LABELS[key] ?? key} htmlFor={`owned-${key}`} changed={changed(key)}>{multiline
    ? <AdminTextarea id={`owned-${key}`} value={String(draft[key] ?? '')} onChange={e => set(key, e.target.value)} rows={key === 'instructions' ? 8 : 3} className="w-full h-auto py-1.5" />
    : <AdminInput id={`owned-${key}`} value={String(draft[key] ?? '')} onChange={e => set(key, e.target.value)} className="w-full" />}</Field>
  const picker = (key: string, objectKind: string, label: string) => <ReferencePicker key={`${key}-${objectKind}`} client={client} kind={objectKind} label={label} value={String(draft[key] ?? '')} onChange={v => set(key, v)} />
  const chips = (key: string, label: string, suggestions: string[], tone?: string) => <Field label={label} changed={changed(key)}><ListField label={label} suggestions={suggestions} tone={tone}
    values={String(draft[key] ?? '').split(',').map(v => v.trim()).filter(Boolean)} onChange={v => set(key, v.join(', '))} /></Field>
  const agentForm = <>
    <Block title="Identity"><div className="space-y-3">
      {input('name')}{input('description', true)}
      {chips('roles', 'Roles', [])}
      <Field label="Enabled" htmlFor="owned-enabled" changed={changed('enabled')}><label className="flex items-center gap-2 dash-body text-fg"><input id="owned-enabled" type="checkbox" checked={draft.enabled === true} onChange={e => set('enabled', e.target.checked)} />{draft.enabled === true ? 'Enabled in desired configuration' : 'Disabled in desired configuration'}</label></Field>
    </div></Block>
    <Block title="Runtime"><div className="grid gap-3 sm:grid-cols-2">
      <Field label="Runtime" htmlFor="owned-runtime" changed={changed('runtime')}><AdminSelect id="owned-runtime" value={String(draft.runtime)} onChange={e => set('runtime', e.target.value)} className="w-full">
        {[...new Set(['', ...PROVIDERS, String(draft.runtime)])].map(p => <option key={p} value={p}>{p || 'From launch profile'}</option>)}</AdminSelect></Field>
      {input('model')}{picker('project_id', 'project', 'Project')}{picker('machine_id', 'machine', 'Machine')}
    </div></Block>
    <Block title="Prompt"><Field label="Instructions" htmlFor="owned-instructions" changed={changed('instructions')}>
      <AdminTextarea id="owned-instructions" value={String(draft.instructions ?? '')} onChange={e => set('instructions', e.target.value)} rows={8} className="w-full h-auto py-1.5" /></Field></Block>
    <Block title="Tools and skills"><div className="space-y-3">
      {chips('tools', 'Tools', KNOWN_TOOLS, ACCENT)}{chips('skills', 'Skills', [], ACCENT)}
    </div></Block>
    <PolicyPreview />
  </>
  const assignmentForm = <>
    <Block title="Assignment"><div className="space-y-3">{input('name')}{input('description', true)}</div></Block>
    <Block title="Assigned to"><div className="grid gap-3 sm:grid-cols-2">
      <Field label="Assignee type" htmlFor="assignee-type" changed={changed('assignee_kind')}><AdminSelect id="assignee-type" className="w-full" value={String(draft.assignee_kind)} onChange={e => { set('assignee_kind', e.target.value); set('assignee_id', '') }}><option value="agent">Agent</option><option value="team">Team</option></AdminSelect></Field>
      {picker('assignee_id', String(draft.assignee_kind), 'Assignee')}
      {input('role')}
    </div></Block>
    <Block title="Work"><div className="grid gap-3 sm:grid-cols-2">
      <Field label="Target source" htmlFor="target-source" changed={changed('target_mode')}><AdminSelect id="target-source" className="w-full" value={String(draft.target_mode)} onChange={e => set('target_mode', e.target.value)}><option value="local">Tachyon record</option><option value="external">External reference</option></AdminSelect></Field>
      {draft.target_mode === 'local' ? <><Field label="Work type" htmlFor="work-type" changed={changed('target_kind')}><AdminSelect id="work-type" className="w-full" value={String(draft.target_kind)} onChange={e => { set('target_kind', e.target.value); set('target_id', '') }}>{[...new Set(['task', 'plan', 'workstream', 'project', 'epic', 'sprint', String(draft.target_kind)])].map(k => <option key={k} value={k}>{k}</option>)}</AdminSelect></Field>{picker('target_id', String(draft.target_kind), 'Assigned work')}</>
        : <>{['provider', 'instance', 'collection', 'external_id'].map(key => input(key))}<p className="dash-support text-fg-faint sm:col-span-2">External references are retained as entered; they are not treated as verified local records.</p></>}
    </div></Block>
    <Block title="Planning"><Field label="Planning status" htmlFor="assignment-state" changed={changed('state')}><AdminSelect id="assignment-state" className="w-full max-w-xs" value={String(draft.state)} onChange={e => set('state', e.target.value)}>{['draft', 'active', 'completed', 'cancelled'].map(v => <option key={v} value={v}>{v}</option>)}</AdminSelect></Field></Block>
  </>
  return <article aria-label={`${kind} editor`}>
    {review ? <Block title="Review changes"><dl className="space-y-3">{Object.entries(builderPatch(kind, draft, base)).map(([key, value]) => <div key={key}><dt className="dash-label text-fg-muted">{OWNED_LABELS[key] ?? key}</dt><dd className="whitespace-pre-wrap break-words dash-body">{value === null ? 'Clear' : typeof value === 'string' ? value || 'Empty' : JSON.stringify(value)}</dd></div>)}</dl><div className="mt-4 flex gap-3"><Btn disabled={busy} onClick={() => setReview(false)}>Continue editing</Btn><Btn tone="primary" disabled={busy || !!issues.length} onClick={() => void save()}>Save {kind}</Btn></div></Block>
    : <>
      {/* The avatar lives in the Station service, not the record, so a locked or busy form does not lock it. */}
      {kind === 'agent' && <Block title="Avatar">{avatar ?? <p className="dash-support text-fg-faint">Set an avatar after the first save.</p>}</Block>}
      <fieldset disabled={busy || locked} className="min-w-0">{kind === 'agent' ? agentForm : assignmentForm}
      <div className="sticky bottom-0 flex flex-wrap items-center gap-3 border-t border-border bg-bg px-5 py-3">
        <span className="font-mono dash-label uppercase tracking-widest text-fg-faint">{base ? `${changes} change${changes === 1 ? '' : 's'}` : `New ${kind}`}</span>
        {issues.length > 0 && <span className="dash-support text-danger">{issues.join(' ')}</span>}
        <span className="ml-auto flex gap-2"><Btn tone="primary" disabled={!dirty || !!issues.length} onClick={() => setReview(true)}>Review {kind}</Btn></span>
      </div></fieldset></>}
    <div className="flex flex-wrap gap-3 px-5 py-3">
      <Btn disabled={busy} onClick={discard}>{base ? 'Discard draft and reload saved version' : 'Discard draft'}</Btn>
      {!base && selected && problem && <Btn disabled={busy} onClick={() => void checkCreate()}>Check whether create was saved</Btn>}
    </div>
  </article>
}

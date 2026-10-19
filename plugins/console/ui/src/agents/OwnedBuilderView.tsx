import { Badge, CopyableId, DetailSection, EmptyState } from '@hollis-labs/design-components'
import { AdminDirectory, AdminFacts, AdminInput, AdminRecordButton, AdminSelect, AdminTextarea, IdentityAvatar } from '@hollis-labs/kit-admin'
import { DataTable, FilterEntityCombobox, RecordSummary } from '@hollis-labs/kit-dashboard/data'
import { DashboardSearchToolbar, DashboardToolbar, TabStrip } from '@hollis-labs/kit-dashboard/layout'
import { StatusBadge } from '@hollis-labs/kit-dashboard/ui'
import { useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { Field } from './AgentEditor'
import { useAgentsClient } from './context'
import { Btn, Chip } from './ui'
import { builderDraft, builderIssues, builderPatch, type BuilderDraft } from './owned-builder-draft'
import { isRetired, newObjectID, ownedBuilderClient, type BuilderKind, type HistoryPage, type ObjectRecord, type OwnedBuilderClient } from './owned-builder-client'
import { useOwnedRecords } from './use-owned-records'
import { AgentPublication } from './AgentPublication'
import { isPrivileged } from './filter'
import type { AgentRecord } from './types'

type Mode = 'details' | 'edit' | 'lifecycle' | 'history'

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

const strings = (value: unknown): string[] => Array.isArray(value) ? value.filter((v): v is string => typeof v === 'string') : []
const text = (value: unknown) => typeof value === 'string' ? value : ''
const ownershipLabel = (row: ObjectRecord) => isRetired(row) ? 'retired' : row.ownership === 'tachyon' ? 'owned' : 'provider observation'
/** Catalog identity keeps avatars stable between observed and adopted definitions. */
const identityOf = (row: ObjectRecord) => row.source?.key.external_id ?? row.id

function Chips({ values }: { values: string[] }) {
  return values.length ? <span className="flex flex-wrap gap-1.5">{values.map(v => <Chip key={v}>{v}</Chip>)}</span> : <span className="text-fg-faint">None reported</span>
}

/** Read-only catalog projection (launch profiles, policy, address) joined by catalog id. Never blocks the owned view. */
function useCatalogJoin() {
  const client = useAgentsClient()
  const [rows, setRows] = useState<AgentRecord[]>([])
  useEffect(() => {
    let live = true
    client.listAgents().then(next => { if (live) setRows(next) }, () => undefined)
    return () => { live = false }
  }, [client])
  return useMemo(() => new Map(rows.map(row => [row.identity.id, row])), [rows])
}

export function OwnedBuilderView({ kind, client = ownedBuilderClient }: { kind: BuilderKind; client?: OwnedBuilderClient }) {
  const records = useOwnedRecords(client, kind)
  const catalog = useCatalogJoin()
  const [selected, setSelected] = useState<string>()
  const [base, setBase] = useState<ObjectRecord>()
  const [draft, setDraft] = useState<BuilderDraft>()
  const [mode, setMode] = useState<Mode>('details')
  const [review, setReview] = useState(false)
  const [busy, setBusy] = useState(false)
  const [problem, setProblem] = useState(''), [message, setMessage] = useState('')
  const [history, setHistory] = useState<HistoryPage>()
  const [filter, setFilter] = useState('')
  const [provider, setProvider] = useState<string | null>(null)
  const pending = useRef(false)
  const dirty = !!draft && (!base || Object.keys(builderPatch(kind, draft, base)).length > 0)
  const issues = draft ? builderIssues(kind, draft) : []
  const locked = !!base && (base.ownership !== 'tachyon' || isRetired(base))
  const newer = base && records.rows.find(row => row.id === base.id && row.revision > base.revision)
  const noun = kind === 'agent' ? 'agent' : 'assignment'
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
    install(row); setMode('details')
  })
  const create = () => { setSelected(newObjectID()); setBase(undefined); setDraft(builderDraft(kind)); setReview(false); setHistory(undefined); setProblem(''); setMessage(''); setMode('edit') }
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

  const providers = useMemo(() => [...new Set([...catalog.values()].flatMap(a => (a.launches ?? []).map(l => l.provider ?? '')).filter(Boolean))].sort(), [catalog])
  const rows = records.rows.filter(row => {
    const joined = catalog.get(identityOf(row))
    return `${row.name} ${strings(row.definition.roles).join(' ')}`.toLowerCase().includes(filter.toLowerCase())
      && (kind !== 'agent' || !provider || text(row.definition.runtime) === provider || !!joined?.launches?.some(l => l.provider === provider))
  })
  const joined = base ? catalog.get(identityOf(base)) : undefined
  const disabled = records.rows.filter(row => row.definition.enabled === false).length

  const title = (row: ObjectRecord): ReactNode => row.name
  const description = (row: ObjectRecord): ReactNode => {
    const joinedRow = catalog.get(identityOf(row))
    const bits: string[] = []
    if (kind === 'agent' && joinedRow?.launches) bits.push(`${joinedRow.launches.length} launch profiles`)
    if (kind === 'agent' && joinedRow && isPrivileged(joinedRow)) bits.push('privileged')
    if (kind === 'agent' && row.definition.enabled === false) bits.push('disabled')
    if (kind === 'assignment') bits.push(text(row.definition.state) || 'draft')
    return bits.join(' · ') || `Revision ${row.revision}`
  }
  /** Provider observations are the default; only the exceptions get a badge. */
  const badge = (row: ObjectRecord): ReactNode => row.ownership === 'tachyon' || isRetired(row)
    ? <span className="font-mono dash-label text-fg-muted">{ownershipLabel(row)}</span> : undefined

  const editor = draft && <>
    {review ? <DetailSection title="Review changes"><dl className="space-y-3">{Object.entries(builderPatch(kind, draft, base)).map(([key, value]) => <div key={key}><dt className="dash-label text-fg-muted">{labels[key] ?? key}</dt><dd className="whitespace-pre-wrap break-words dash-body">{value === null ? 'Clear' : typeof value === 'string' ? value || 'Empty' : JSON.stringify(value)}</dd></div>)}</dl><div className="mt-4 flex gap-3"><Btn disabled={busy} onClick={() => setReview(false)}>Continue editing</Btn><Btn tone="primary" disabled={busy || !!issues.length} onClick={() => void save()}>Save {kind}</Btn></div></DetailSection>
    : <fieldset disabled={busy || locked} className="min-w-0"><DetailSection title={base ? 'Definition' : `New ${kind}`}><div className="grid gap-4 sm:grid-cols-2">
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
    </div></DetailSection><div className="flex flex-wrap items-center gap-3 pt-3"><Btn tone="primary" disabled={!dirty || !!issues.length} onClick={() => setReview(true)}>Review {kind}</Btn>{issues.length > 0 && <p className="dash-support text-fg-muted">{issues.join(' ')}</p>}</div></fieldset>}
    <div className="flex flex-wrap gap-3 pt-3">
      <Btn disabled={busy} onClick={() => base ? void open(base.id) : (setDraft(undefined), setSelected(undefined), setReview(false))}>{base ? 'Discard draft and reload saved version' : 'Discard draft'}</Btn>
      {!base && selected && problem && <Btn disabled={busy} onClick={() => void checkCreate()}>Check whether create was saved</Btn>}
    </div>
  </>

  const status = base && <StatusBadge status={isRetired(base) ? 'Retired' : base.definition.enabled === false ? 'Disabled' : 'Enabled'} tone={isRetired(base) || base.definition.enabled === false ? 'paused' : 'done'} />

  const details = base && <div className="dash-form-stack">
    <DashboardToolbar>
      {kind === 'agent' && <IdentityAvatar identity={identityOf(base)} size="large" />}
      <div className="min-w-0 flex-1">
        <h2 className="dash-record-title break-words">{base.name}</h2>
        <CopyableId id={identityOf(base)} label={identityOf(base)} />
      </div>
      <Badge variant="secondary">{ownershipLabel(base)} · rev {base.revision}</Badge>
      {kind === 'agent' && status}
    </DashboardToolbar>
    {text(base.definition.description) && <p className="dash-support">{text(base.definition.description)}</p>}
    {kind === 'agent' ? <>
      <DetailSection title="Identity">
        <AdminFacts items={[
          { label: 'Address', value: joined?.identity.urn },
          { label: 'Roles', value: <Chips values={strings(base.definition.roles)} /> },
          { label: 'Source', value: base.source ? `${base.source.key.provider} · ${base.source.key.external_id}` : 'Created in Tachyon' },
        ]} />
      </DetailSection>
      {(['runtime', 'model', 'project_id', 'machine_id'] as const).some(key => text(base.definition[key])) && <DetailSection title="Runtime placement">
        <AdminFacts items={[
          { label: 'Runtime', value: text(base.definition.runtime) || undefined },
          { label: 'Model', value: text(base.definition.model) || undefined },
          { label: 'Project', value: text(base.definition.project_id) || undefined },
          { label: 'Machine', value: text(base.definition.machine_id) || undefined },
        ]} />
      </DetailSection>}
      {joined?.launches && <DetailSection title="Launch profiles">
        <p className="dash-support text-fg-faint pb-2">Runtime belongs to launch profiles. This definition does not imply a running agent.</p>
        <DataTable items={joined.launches} getRowId={l => l.id} pageSize={200}
          columns={[
            { key: 'id', header: 'Launch', width: 'fill', className: 'min-w-48', cell: l => <RecordSummary title={l.id} metadata={l.project ?? 'Project not reported'} /> },
            { key: 'provider', header: 'Provider', cell: l => l.provider ?? 'Not reported' },
            { key: 'state', header: 'State', cell: l => l.enabled === undefined ? 'Not reported' : <StatusBadge status={l.enabled ? 'Enabled' : 'Disabled'} tone={l.enabled ? 'done' : 'paused'} /> },
          ]}
          emptyState={<EmptyState title="No launch profiles" description="No profile references this definition." variant="empty" />} />
      </DetailSection>}
      <DetailSection title="Tools and skills">
        <AdminFacts items={[
          { label: 'MCP servers', value: <Chips values={strings(base.definition.tools).length ? strings(base.definition.tools) : joined?.tools ?? []} /> },
          { label: 'Skills', value: <Chips values={strings(base.definition.skills)} /> },
        ]} />
      </DetailSection>
      {joined && <DetailSection title="Permissions and policy">
        <AdminFacts items={[
          { label: 'Permission mode', value: joined.policy.permissionMode },
          { label: 'Sandbox', value: joined.policy.sandbox },
          { label: 'Network', value: joined.policy.network === undefined ? undefined : joined.policy.network ? 'Online' : 'Offline' },
          { label: 'Scopes', value: <Chips values={joined.policy.scopes} /> },
        ]} />
      </DetailSection>}
      {text(base.definition.instructions) && <DetailSection title="Instructions"><pre className="whitespace-pre-wrap break-words dash-support text-fg-muted">{text(base.definition.instructions)}</pre></DetailSection>}
    </> : <DetailSection title="Assignment">
      <AdminFacts items={[
        { label: 'Assignee', value: text(base.definition.team_id) ? `Team ${text(base.definition.team_id)}` : text(base.definition.agent_id) ? `Agent ${text(base.definition.agent_id)}` : undefined },
        { label: 'Work', value: (base.definition.target as { kind?: string; id?: string } | undefined)?.id ? `${(base.definition.target as { kind?: string }).kind} ${(base.definition.target as { id: string }).id}` : base.definition.external_target ? 'External reference' : undefined },
        { label: 'Role', value: text(base.definition.role) || undefined },
        { label: 'Planning status', value: text(base.definition.state) || 'draft' },
      ]} />
      <p className="dash-support text-fg-faint pt-2">Saving an assignment does not dispatch it.</p>
    </DetailSection>}
  </div>

  const lifecyclePane = base && <div className="dash-form-stack">
    <DetailSection title="Lifecycle">
      {dirty && <p className="dash-support text-fg-muted pb-2">Save or discard your edits before changing lifecycle.</p>}
      {locked && !isRetired(base) && <p className="dash-body text-fg-muted pb-2">This provider observation is read-only. Adopt it to manage its definition in Tachyon.</p>}
      {isRetired(base) && <p className="dash-body text-fg-muted pb-2">This record is retired. History is retained.</p>}
      <div className="flex flex-wrap gap-3">
        {!dirty && (base.ownership === 'tachyon'
          ? <Btn disabled={busy} onClick={() => void lifecycle(base.deleted_at ? 'restore' : 'delete')}>{base.deleted_at ? 'Restore' : 'Retire'} {kind}</Btn>
          : !isRetired(base) && <Btn disabled={busy} onClick={() => void lifecycle('take_ownership')}>Adopt definition into Tachyon</Btn>)}
      </div>
    </DetailSection>
    {kind === 'agent' && base.ownership === 'tachyon' && <AgentPublication key={base.id} agent={base} disabled={dirty || busy || isRetired(base)} />}
  </div>

  const historyPane = base && <div className="dash-form-stack">
    <DetailSection title="Saved history">
      {history ? <ul>{history.items.map(item => <li key={item.record.revision} className="py-2 dash-support">Revision {item.record.revision} · {item.operation} · {item.actor} · {item.recorded_at}</li>)}</ul>
        : <p className="dash-support text-fg-muted pb-2">History is read on demand.</p>}
      <div className="flex gap-3 pt-2">
        {!history && <Btn disabled={busy} onClick={() => void readHistory()}>Load history</Btn>}
        {history?.next_after_revision && <Btn disabled={busy} onClick={() => void readHistory()}>More history</Btn>}
      </div>
    </DetailSection>
  </div>

  return <div>
    <DashboardToolbar className="px-4 py-3 border-b border-border">
      <p className="dash-support flex-1">{kind === 'agent' ? 'Agent definitions owned by Tachyon. Saving or enabling a definition does not start or change a running agent.' : 'Planned work for an agent or team. Saving an assignment does not dispatch it.'}</p>
      <Btn onClick={create} disabled={busy || dirty}>New {noun}</Btn><Btn disabled={busy || records.loading} onClick={() => void records.refresh()}>Refresh</Btn>
    </DashboardToolbar>
    <DashboardToolbar className="px-4 py-2 border-b border-border">
      <span className="dash-support">{records.rows.length} {noun}s{kind === 'agent' && ` · ${disabled} disabled`}{records.next ? ' · more available' : ''}</span>
    </DashboardToolbar>
    {records.error && <p role="alert" className="px-4 py-2 dash-body text-danger">{records.error} Showing the last loaded records.</p>}
    {problem && <p role="alert" className="px-4 py-2 dash-body text-danger">{problem}</p>}
    {message && <p role="status" className="px-4 py-2 dash-body text-success">{message}</p>}
    <AdminDirectory label={`${kind} definitions`} layout="wide"
      controls={<DashboardSearchToolbar value={filter} onChange={setFilter} label="Filter loaded records" placeholder={`Search ${noun}s…`}>
        {kind === 'agent' && providers.length > 0 && <FilterEntityCombobox icon={null} ariaLabel="Filter definition provider" allLabel="All providers" value={provider} onChange={setProvider} items={providers.map(p => ({ id: p, name: p }))} />}
      </DashboardSearchToolbar>}
      records={<div>
        {rows.length || records.loading ? <ul aria-label={`${kind} records`}>{rows.map(row => <li key={row.id}><AdminRecordButton selected={selected === row.id} muted={row.definition.enabled === false || isRetired(row)} onSelect={() => { if (!busy && !dirty) void open(row.id) }}
          avatar={kind === 'agent' ? <IdentityAvatar identity={identityOf(row)} /> : undefined} title={title(row)} description={description(row)} badge={badge(row)} /></li>)}</ul>
          : records.rows.length
            ? <EmptyState title={`No ${noun}s match`} description="Clear the search or provider filter." variant="empty" />
            : <EmptyState title={`No ${noun}s yet`} description={kind === 'agent' ? 'Create an agent definition to manage it in Tachyon.' : 'Plan work for an agent or team with New assignment. Saving does not dispatch it.'} variant="empty" />}
        {records.loading && <p role="status" className="px-4 py-2 dash-support">Refreshing…</p>}
        {records.next && <div className="px-4 py-2"><Btn disabled={records.loading} onClick={() => void records.more()}>Load more {kind}s</Btn></div>}
      </div>}
      detail={draft ? <article aria-label={`${kind} editor`}>
        {base ? <TabStrip value={mode} onChange={setMode} tabs={[{ key: 'details', label: 'Details' }, { key: 'edit', label: 'Edit' }, { key: 'lifecycle', label: 'Lifecycle' }, { key: 'history', label: 'History' }]} /> : null}
        <div className="dash-form-stack">
          {newer && <p role="status" className="dash-body text-warning">A newer revision is available. Your draft is preserved.</p>}
          {dirty && base && <p className="dash-support text-fg-muted">Save or discard your edits before selecting another record.</p>}
          {mode === 'details' && base ? details : mode === 'lifecycle' && base ? lifecyclePane : mode === 'history' && base ? historyPane : editor}
        </div>
      </article> : <div className="dash-form-stack"><p className="dash-support">Select a saved {noun} or create one.</p></div>} />
  </div>
}

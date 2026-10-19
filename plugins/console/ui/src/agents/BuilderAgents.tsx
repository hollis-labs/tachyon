import { AdminDirectory, AdminFact, AdminInput, AdminSection as Section, AdminSelect, AdminTabs } from '@hollis-labs/kit-admin'
import { EmptyState } from '@hollis-labs/design-components'
import { DashboardToolbar } from '@hollis-labs/kit-dashboard/layout'
import { dataRefreshRegistry } from '@tachyon/host-sdk'
import { useEffect, useMemo, useState } from 'react'
import { AgentDetail, Row } from './AgentDirectory'
import { OwnedEditorForm } from './OwnedEditorForm'
import { OwnedHistoryPane, OwnedLifecyclePane, ownershipLabel } from './OwnedPanes'
import { useAgentsClient } from './context'
import { filterAgents } from './filter'
import { isRetired, ownedBuilderClient, type ObjectRecord, type OwnedBuilderClient } from './owned-builder-client'
import { useOwnedEditor } from './use-owned-editor'
import { Btn } from './ui'
import type { AgentRecord } from './types'

type PaneMode = 'view' | 'edit' | 'lifecycle' | 'history'

const text = (value: unknown) => typeof value === 'string' ? value : ''
const strings = (value: unknown): string[] => Array.isArray(value) ? value.filter((v): v is string => typeof v === 'string') : []
/** Catalog identity keeps avatars and launch joins stable between observed and adopted definitions. */
export const identityOf = (row: ObjectRecord) => row.source?.key.external_id ?? row.id

/** One agent as the directory shows it: the owned record, enriched with read-only catalog data when the ids line up. */
export function agentView(row: ObjectRecord, catalog: Map<string, AgentRecord>): AgentRecord {
  const cat = catalog.get(identityOf(row))
  const d = row.definition
  const owned = row.ownership === 'tachyon'
  const base: AgentRecord = cat ?? {
    identity: { id: identityOf(row), name: row.name, roles: [], enabled: true },
    runtime: { provider: '' }, policy: { permissionMode: 'default', scopes: [], rules: [] },
    tools: [], skills: [], teams: [], source: { path: `Tachyon · ${row.id}` }, launches: [],
  }
  return {
    ...base,
    identity: { ...base.identity, name: row.name, enabled: d.enabled !== false, retired: isRetired(row),
      roles: owned || !cat ? strings(d.roles) : base.identity.roles, summary: text(d.description) || base.identity.summary },
    runtime: { ...base.runtime, provider: text(d.runtime) || base.runtime.provider, model: text(d.model) || base.runtime.model, project: text(d.project_id) || base.runtime.project },
    skills: owned || !cat ? strings(d.skills) : base.skills,
    tools: owned && strings(d.tools).length ? strings(d.tools) : base.tools,
    source: { ...base.source, revision: `revision ${row.revision}` },
  }
}

/** Read-only catalog projection (launch profiles, policy, address). It never blocks or fails the owned list. */
function useCatalog() {
  const client = useAgentsClient()
  const [rows, setRows] = useState<AgentRecord[]>([])
  const [version, setVersion] = useState(0)
  useEffect(() => dataRefreshRegistry('catalog').subscribe(() => setVersion(v => v + 1)), [])
  useEffect(() => {
    let live = true
    client.listAgents().then(next => { if (live) setRows(next) }, () => undefined)
    return () => { live = false }
  }, [client, version])
  return useMemo(() => new Map(rows.map(row => [row.identity.id, row])), [rows])
}

/** Builder > Agents: Tachyon-owned agent definitions in the shared directory layout. */
export function BuilderAgents({ client = ownedBuilderClient, now: nowProp }: { client?: OwnedBuilderClient; now?: number }) {
  const editor = useOwnedEditor('agent', client)
  const { records, base, draft, busy, dirty, problem, message, newer } = editor
  const catalog = useCatalog()
  const [search, setSearch] = useState('')
  const [provider, setProvider] = useState('')
  const [mode, setMode] = useState<PaneMode>('view')
  const [showRetired, setShowRetired] = useState(false)
  const [now] = useState(() => nowProp ?? Date.now())

  const views = useMemo(() => records.rows.map(row => ({ row, view: agentView(row, catalog) })), [records.rows, catalog])
  const providers = useMemo(() => [...new Set(views.flatMap(({ view }) => [view.runtime.provider, ...(view.launches ?? []).map(l => l.provider ?? '')]).filter(Boolean))].sort(), [views])
  const rows = useMemo(() => {
    const keep = new Set(filterAgents(views.map(v => v.view), { search, provider }).map(v => v.identity.id))
    return views.filter(({ row, view }) => keep.has(view.identity.id) && (showRetired || !isRetired(row) || row.id === editor.selected))
      .sort((a, b) => a.view.identity.name.localeCompare(b.view.identity.name))
  }, [views, search, provider, showRetired, editor.selected])

  // Like the catalog directory, something is always selected once records exist.
  useEffect(() => {
    if (!editor.selected && !draft && records.rows.length) void editor.open(rows[0]?.row.id ?? records.rows[0].id)
  }, [editor.selected, draft, records.rows.length])

  const select = (id: string) => { if (!busy && !dirty) { setMode('view'); void editor.open(id) } }
  const creating = !!draft && !base
  const selectedView = base && views.find(v => v.row.id === base.id)?.view
  const disabled = views.filter(({ view }) => !view.identity.enabled && !view.identity.retired).length
  const retired = views.filter(({ view }) => view.identity.retired).length

  return (
    <div>
      <div className="flex flex-wrap items-center gap-x-6 gap-y-1 border-b border-border/80 px-4 py-2 font-mono dash-label uppercase tracking-widest text-fg-faint">
        <span>{views.length} agent definitions</span>
        <span>{disabled} disabled</span>
        <span>{retired} retired</span>
        <span className="ml-auto text-fg-muted">Tachyon agent definitions</span>
      </div>
      <p className="border-b border-border/80 px-4 py-3 dash-body text-fg-muted">Agent definitions owned by Tachyon. Saving or enabling a definition does not start or change a running agent. Live agents and processes are managed on Agents.</p>
      {records.error && <p role="alert" className="border-b border-border/80 px-4 py-2 dash-body text-danger">{records.error} Showing the last loaded records.</p>}
      {problem && <p role="alert" className="border-b border-border/80 px-4 py-2 dash-body text-danger">{problem}</p>}
      {message && <p role="status" className="border-b border-border/80 px-4 py-2 dash-body text-success">{message}</p>}
      <DashboardToolbar className="border-b border-border px-4 py-2">
        <AdminInput value={search} onChange={e => setSearch(e.target.value)} placeholder="Search agents" aria-label="Search agents" className="max-w-xs" />
        <AdminSelect value={provider} onChange={e => setProvider(e.target.value)} aria-label="Filter by provider" className="w-auto">
          <option value="">All providers</option>
          {providers.map(p => <option key={p} value={p}>{p}</option>)}
        </AdminSelect>
        <label className="flex items-center gap-2 font-mono dash-label uppercase tracking-widest text-fg-faint"><input type="checkbox" checked={showRetired} onChange={e => setShowRetired(e.target.checked)} /> Show retired</label>
        <span className="ml-auto flex gap-2"><Btn disabled={busy || records.loading} onClick={() => void records.refresh()}>Refresh</Btn><Btn tone="primary" disabled={busy || dirty} onClick={() => { setMode('edit'); editor.create() }}>New agent</Btn></span>
      </DashboardToolbar>
      <AdminDirectory label="Agent definitions" layout="wide" records={
        rows.length === 0 ? (records.loading ? <div className="p-4 font-mono dash-support text-fg-faint">Loading agents…</div>
          : <div className="p-4"><EmptyState variant="empty" title={views.length ? 'No agents match' : 'No agents yet'} description={views.length ? 'Clear the search or provider filter.' : 'Create an agent definition to manage it in Tachyon.'} /></div>)
          : <div>
            <ul aria-label="Agents">
              {rows.map(({ row, view }) => <Row key={row.id} agent={view} selected={row.id === editor.selected} onSelect={() => select(row.id)} />)}
            </ul>
            {records.next && <div className="px-4 py-3"><Btn disabled={records.loading} onClick={() => void records.more()}>Load more agents</Btn></div>}
          </div>
      } detail={creating ? (
        <div>
          <AdminTabs label="Agent pane" value="edit" onChange={() => undefined} items={[{ key: 'edit', label: 'New agent' }]} />
          <OwnedEditorForm editor={editor} />
        </div>
      ) : base && selectedView ? (
        <div>
          <AdminTabs label="Agent pane" value={mode} onChange={setMode}
            items={[{ key: 'view', label: 'Details' }, { key: 'edit', label: 'Edit' }, { key: 'lifecycle', label: 'Lifecycle' }, { key: 'history', label: 'History' }]} />
          {newer && <p role="status" className="border-b border-border/80 px-5 py-2 dash-body text-warning">A newer revision is available. Your draft is preserved.</p>}
          {dirty && <p className="border-b border-border/80 px-5 py-2 dash-support text-fg-muted">Save or discard your edits before selecting another record.</p>}
          {mode === 'view' && <>
            <AgentDetail agent={selectedView} now={now} />
            <Section title="Tachyon configuration">
              <dl>
                <AdminFact label="Ownership">{ownershipLabel(base)}</AdminFact>
                <AdminFact label="Revision"><span className="font-mono dash-support">{base.revision}</span></AdminFact>
                {text(base.definition.runtime) && <AdminFact label="Runtime">{text(base.definition.runtime)}</AdminFact>}
                {text(base.definition.machine_id) && <AdminFact label="Machine"><span className="font-mono dash-support">{text(base.definition.machine_id)}</span></AdminFact>}
              </dl>
              {base.ownership !== 'tachyon' && !isRetired(base) && <p className="mt-2 dash-support text-fg-faint">Provider observations are read-only. Adopt the definition under Lifecycle to manage it in Tachyon.</p>}
            </Section>
          </>}
          {mode === 'edit' && <OwnedEditorForm editor={editor} />}
          {mode === 'lifecycle' && <OwnedLifecyclePane editor={editor} />}
          {mode === 'history' && <OwnedHistoryPane editor={editor} />}
        </div>
      ) : <div className="p-6 dash-body text-fg-muted">Select a saved agent or create one.</div>} />
    </div>
  )
}

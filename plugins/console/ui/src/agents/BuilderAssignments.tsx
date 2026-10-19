import { AdminDirectory, AdminFact, AdminInput, AdminRecordButton, AdminSection as Section, AdminTabs } from '@hollis-labs/kit-admin'
import { EmptyState } from '@hollis-labs/design-components'
import { DashboardToolbar } from '@hollis-labs/kit-dashboard/layout'
import { useMemo, useState } from 'react'
import { OwnedEditorForm } from './OwnedEditorForm'
import { OwnedHistoryPane, OwnedLifecyclePane, ownershipLabel } from './OwnedPanes'
import { isRetired, ownedBuilderClient, type ObjectRecord, type OwnedBuilderClient } from './owned-builder-client'
import { useOwnedEditor } from './use-owned-editor'
import { Btn, Chip } from './ui'

type PaneMode = 'view' | 'edit' | 'lifecycle' | 'history'
const text = (value: unknown) => typeof value === 'string' ? value : ''
const target = (row: ObjectRecord) => {
  const local = row.definition.target as { kind?: string; id?: string } | undefined
  const external = row.definition.external_target as { provider?: string; external_id?: string } | undefined
  return local?.id ? `${local.kind} ${local.id}` : external?.external_id ? `${external.provider} ${external.external_id}` : undefined
}
const assignee = (row: ObjectRecord) => text(row.definition.team_id) ? `team ${text(row.definition.team_id)}` : text(row.definition.agent_id) ? `agent ${text(row.definition.agent_id)}` : undefined

/** Builder > Assignments: planned work for an agent or team. Saving never dispatches it. */
export function BuilderAssignments({ client = ownedBuilderClient }: { client?: OwnedBuilderClient }) {
  const editor = useOwnedEditor('assignment', client)
  const { records, base, draft, busy, dirty, problem, message, newer } = editor
  const [search, setSearch] = useState('')
  const [mode, setMode] = useState<PaneMode>('view')
  const rows = useMemo(() => records.rows.filter(row => `${row.name} ${text(row.definition.role)} ${target(row) ?? ''}`.toLowerCase().includes(search.toLowerCase())), [records.rows, search])
  const creating = !!draft && !base
  const select = (id: string) => { if (!busy && !dirty) { setMode('view'); void editor.open(id) } }
  return (
    <div>
      <div className="flex flex-wrap items-center gap-x-6 gap-y-1 border-b border-border/80 px-4 py-2 font-mono dash-label uppercase tracking-widest text-fg-faint">
        <span>{records.rows.length} assignments</span>
        <span>{records.rows.filter(r => text(r.definition.state) === 'active').length} active</span>
        <span className="ml-auto text-fg-muted">Tachyon assignment plans</span>
      </div>
      <p className="border-b border-border/80 px-4 py-3 dash-body text-fg-muted">Planned work for an agent or team. Saving an assignment does not dispatch it, and target references are not verified against live work.</p>
      {records.error && <p role="alert" className="border-b border-border/80 px-4 py-2 dash-body text-danger">{records.error} Showing the last loaded records.</p>}
      {problem && <p role="alert" className="border-b border-border/80 px-4 py-2 dash-body text-danger">{problem}</p>}
      {message && <p role="status" className="border-b border-border/80 px-4 py-2 dash-body text-success">{message}</p>}
      <DashboardToolbar className="border-b border-border px-4 py-2">
        <AdminInput value={search} onChange={e => setSearch(e.target.value)} placeholder="Search assignments" aria-label="Search assignments" className="max-w-xs" />
        <span className="ml-auto flex gap-2"><Btn disabled={busy || records.loading} onClick={() => void records.refresh()}>Refresh</Btn><Btn tone="primary" disabled={busy || dirty} onClick={() => { setMode('edit'); editor.create() }}>New assignment</Btn></span>
      </DashboardToolbar>
      <AdminDirectory label="Assignments" layout="wide" records={
        rows.length === 0 ? <div className="p-4"><EmptyState variant="empty" title={records.rows.length ? 'No assignments match' : 'No assignments yet'} description={records.rows.length ? 'Clear the search.' : 'Plan work for an agent or team with New assignment. Saving does not dispatch it.'} /></div>
          : <div>
            <ul aria-label="Assignments">
              {rows.map(row => <li key={row.id}><AdminRecordButton selected={row.id === editor.selected} muted={isRetired(row)} size="comfortable" onSelect={() => select(row.id)}
                title={<span className="flex flex-wrap items-center gap-x-3 gap-y-0.5">{row.name}{isRetired(row) && <Chip tone="text-danger">retired</Chip>}</span>}
                description={[assignee(row), target(row), text(row.definition.role)].filter(Boolean).join(' · ') || `Revision ${row.revision}`}
                badge={<span className="font-mono dash-label text-fg-muted">{text(row.definition.state) || 'draft'}</span>} /></li>)}
            </ul>
            {records.next && <div className="px-4 py-3"><Btn disabled={records.loading} onClick={() => void records.more()}>Load more assignments</Btn></div>}
          </div>
      } detail={creating ? (
        <div>
          <AdminTabs label="Assignment pane" value="edit" onChange={() => undefined} items={[{ key: 'edit', label: 'New assignment' }]} />
          <OwnedEditorForm editor={editor} />
        </div>
      ) : base ? (
        <div>
          <AdminTabs label="Assignment pane" value={mode} onChange={setMode}
            items={[{ key: 'view', label: 'Details' }, { key: 'edit', label: 'Edit' }, { key: 'lifecycle', label: 'Lifecycle' }, { key: 'history', label: 'History' }]} />
          {newer && <p role="status" className="border-b border-border/80 px-5 py-2 dash-body text-warning">A newer revision is available. Your draft is preserved.</p>}
          {dirty && <p className="border-b border-border/80 px-5 py-2 dash-support text-fg-muted">Save or discard your edits before selecting another record.</p>}
          {mode === 'view' && <article aria-label={`${base.name} detail`}>
            <header className="border-b border-border/80 px-5 py-4">
              <h2 className="truncate font-mono dash-detail-title font-semibold uppercase tracking-wide">{base.name}</h2>
              {text(base.definition.description) && <p className="mt-1 dash-body text-fg-muted">{text(base.definition.description)}</p>}
            </header>
            <Section title="Assignment">
              <dl>
                <AdminFact label="Assignee">{assignee(base) ?? <span className="text-fg-faint">not set</span>}</AdminFact>
                <AdminFact label="Work">{target(base) ?? <span className="text-fg-faint">not set</span>}</AdminFact>
                <AdminFact label="Role">{text(base.definition.role) || <span className="text-fg-faint">not set</span>}</AdminFact>
                <AdminFact label="Planning status">{text(base.definition.state) || 'draft'}</AdminFact>
              </dl>
            </Section>
            <Section title="Record">
              <dl>
                <AdminFact label="Ownership">{ownershipLabel(base)}</AdminFact>
                <AdminFact label="Revision"><span className="font-mono dash-support">{base.revision}</span></AdminFact>
              </dl>
            </Section>
          </article>}
          {mode === 'edit' && <OwnedEditorForm editor={editor} />}
          {mode === 'lifecycle' && <OwnedLifecyclePane editor={editor} />}
          {mode === 'history' && <OwnedHistoryPane editor={editor} />}
        </div>
      ) : <div className="p-6 dash-body text-fg-muted">Select a saved assignment or create one.</div>} />
    </div>
  )
}

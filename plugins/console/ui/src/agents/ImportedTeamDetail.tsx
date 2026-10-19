import { Badge, CopyableId, DetailSection } from '@hollis-labs/design-components'
import { AdminFacts } from '@hollis-labs/kit-admin'
import { DataTable, RecordSummary } from '@hollis-labs/kit-dashboard/data'
import { DashboardToolbar } from '@hollis-labs/kit-dashboard/layout'
import type { DefinitionPin } from './team-config'

export interface ImportedTeam {
  id: string
  name: string
  version: number
  slots: { name: string; role: string; definition_ref?: DefinitionPin; resolution?: string; activation?: string; required?: boolean; min?: number; max?: number }[]
  routing?: { coordinator_slot?: string }
  policy?: { history?: string; spawn?: Record<string, unknown> }
  authority?: { mode?: string; grants?: unknown[] }
  [field: string]: unknown
}
export interface ImportedTeamSnapshot { source: string; imported_at: string; teams: ImportedTeam[] }
export const importedTeamKey = (team: ImportedTeam) => `import:${team.id}@${team.version}`

export function ImportedTeamDetail({ team, snapshot }: { team: ImportedTeam; snapshot: ImportedTeamSnapshot }) {
  return <article aria-label={`${team.id} version ${team.version} snapshot`} className="dash-form-stack">
    <DashboardToolbar>
      <h2 className="dash-record-title min-w-0 flex-1 break-words">{team.name}</h2>
      <Badge variant="secondary">Imported · read only</Badge>
    </DashboardToolbar>
    <div className="flex flex-wrap items-center gap-3"><CopyableId id={team.id} label={team.id} /><span className="dash-support text-fg-faint">version {team.version}</span></div>
    <p className="dash-support text-fg-muted">Copied from {snapshot.source} on <time dateTime={snapshot.imported_at}>{new Date(snapshot.imported_at).toLocaleString()}</time>. This is a saved definition, not current running membership.</p>
    <DetailSection title={`Seats (${team.slots.length})`}>
      <DataTable items={team.slots} getRowId={(slot) => slot.name} pageSize={200}
        columns={[
          { key: 'name', header: 'Seat', width: 'fill', className: 'min-w-48', cell: (slot) => <RecordSummary title={slot.name} metadata={slot.role || 'No role specified'} /> },
          { key: 'definition', header: 'Definition pin', cell: (slot) => <span className="block min-w-56 break-all whitespace-normal">{slot.definition_ref ? `${slot.definition_ref.id}@${slot.definition_ref.revision}` : 'No definition pin'}{[slot.resolution, slot.activation].filter(Boolean).length > 0 && <span className="block dash-support text-fg-faint">{[slot.resolution, slot.activation].filter(Boolean).join(' · ')}</span>}</span> },
          { key: 'members', header: 'Members', cell: (slot) => `${slot.required ? 'Required' : 'Optional'} · ${slot.min ?? 0}–${slot.max ?? 1}` },
        ]} />
    </DetailSection>
    <DetailSection title="Shared policy">
      <AdminFacts items={[
        { label: 'Coordinator', value: team.routing?.coordinator_slot || 'Not specified' },
        { label: 'Authority', value: team.authority?.mode || 'Not specified' },
        { label: 'History sharing', value: team.policy?.history || 'Not specified' },
      ]} />
    </DetailSection>
    <DetailSection title="Original definition"><details><summary className="cursor-pointer dash-body text-fg-muted">View complete saved configuration</summary><pre className="mt-3 max-h-96 overflow-auto whitespace-pre-wrap break-all dash-support text-fg-muted">{JSON.stringify(team, null, 2)}</pre></details></DetailSection>
  </article>
}

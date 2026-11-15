import { Block, Chip } from './ui'
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
  return <article aria-label={`${team.id} version ${team.version} snapshot`}>
    <Block title="Saved team snapshot">
      <div className="flex flex-wrap items-center gap-3"><h2 className="dash-detail-title font-semibold text-fg">{team.name}</h2><Chip tone="border-info/50 text-info">Imported · read only</Chip></div>
      <p className="mt-1 font-mono dash-support text-fg-muted">{team.id} · version {team.version}</p>
      <p className="mt-3 dash-body text-fg-muted">Copied from {snapshot.source} on <time dateTime={snapshot.imported_at}>{new Date(snapshot.imported_at).toLocaleString()}</time>.</p>
      <p className="mt-2 dash-support text-fg-faint">Manual snapshot pending data access. This is a saved definition, not current running membership. Refresh checks the local copy.</p>
    </Block>
    <Block title={`Seats (${team.slots.length})`}>
      <ul className="space-y-3">{team.slots.map((slot, index) => <li key={`${slot.name}-${index}`} className="border border-border p-3">
        <div className="flex flex-wrap items-center gap-2"><span className="font-mono dash-body text-fg">{slot.name}</span><Chip>{slot.role || 'No role specified'}</Chip><span className="dash-support text-fg-muted">{slot.required ? 'Required' : 'Optional'} · {slot.min ?? 0}–{slot.max ?? 1} members</span></div>
        <p className="mt-2 break-all font-mono dash-support text-fg-muted">{slot.definition_ref ? `${slot.definition_ref.id}@${slot.definition_ref.revision}` : 'No definition pin'}</p>
        {slot.definition_ref?.digest && <p className="mt-1 break-all font-mono dash-label text-fg-faint">{slot.definition_ref.digest}</p>}
        <p className="mt-2 dash-support text-fg-muted">{[slot.resolution, slot.activation].filter(Boolean).join(' · ')}</p>
      </li>)}</ul>
    </Block>
    <Block title="Shared policy"><dl className="grid gap-x-4 gap-y-2 dash-body sm:grid-cols-2"><dt className="text-fg-faint">Coordinator</dt><dd>{team.routing?.coordinator_slot || 'Not specified'}</dd><dt className="text-fg-faint">Authority</dt><dd>{team.authority?.mode || 'Not specified'}</dd><dt className="text-fg-faint">History sharing</dt><dd>{team.policy?.history || 'Not specified'}</dd></dl></Block>
    <Block title="Original definition"><details><summary className="cursor-pointer dash-body text-fg-muted">View complete saved configuration</summary><pre className="mt-3 max-h-96 overflow-auto whitespace-pre-wrap break-all dash-support text-fg-muted">{JSON.stringify(team, null, 2)}</pre></details></Block>
  </article>
}

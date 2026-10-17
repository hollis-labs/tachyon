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
      <div className="flex flex-wrap items-center gap-3"><h2 className="text-lg font-semibold text-zinc-100">{team.name}</h2><Chip tone="border-sky-400/50 text-sky-300">Imported · read only</Chip></div>
      <p className="mt-1 font-mono text-xs text-zinc-400">{team.id} · version {team.version}</p>
      <p className="mt-3 text-sm text-zinc-400">Copied from {snapshot.source} on <time dateTime={snapshot.imported_at}>{new Date(snapshot.imported_at).toLocaleString()}</time>.</p>
      <p className="mt-2 text-xs text-zinc-500">Manual snapshot pending data access. This is a saved definition, not current running membership. Refresh checks the local copy.</p>
    </Block>
    <Block title={`Seats (${team.slots.length})`}>
      <ul className="space-y-3">{team.slots.map((slot, index) => <li key={`${slot.name}-${index}`} className="border border-zinc-800 p-3">
        <div className="flex flex-wrap items-center gap-2"><span className="font-mono text-sm text-zinc-100">{slot.name}</span><Chip>{slot.role || 'No role specified'}</Chip><span className="text-xs text-zinc-400">{slot.required ? 'Required' : 'Optional'} · {slot.min ?? 0}–{slot.max ?? 1} members</span></div>
        <p className="mt-2 break-all font-mono text-xs text-zinc-300">{slot.definition_ref ? `${slot.definition_ref.id}@${slot.definition_ref.revision}` : 'No definition pin'}</p>
        {slot.definition_ref?.digest && <p className="mt-1 break-all font-mono text-[11px] text-zinc-500">{slot.definition_ref.digest}</p>}
        <p className="mt-2 text-xs text-zinc-400">{[slot.resolution, slot.activation].filter(Boolean).join(' · ')}</p>
      </li>)}</ul>
    </Block>
    <Block title="Shared policy"><dl className="grid gap-x-4 gap-y-2 text-sm sm:grid-cols-[10rem_1fr]"><dt className="text-zinc-500">Coordinator</dt><dd>{team.routing?.coordinator_slot || 'Not specified'}</dd><dt className="text-zinc-500">Authority</dt><dd>{team.authority?.mode || 'Not specified'}</dd><dt className="text-zinc-500">History sharing</dt><dd>{team.policy?.history || 'Not specified'}</dd></dl></Block>
    <Block title="Original definition"><details><summary className="cursor-pointer text-sm text-zinc-300">View complete saved configuration</summary><pre className="mt-3 max-h-96 overflow-auto whitespace-pre-wrap break-all text-xs text-zinc-400">{JSON.stringify(team, null, 2)}</pre></details></Block>
  </article>
}

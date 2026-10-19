import { operatorFetch } from '../operator/elevation'
import type { TeamConfigDraft, teamPreview } from './team-config'

type Preview = ReturnType<typeof teamPreview>
export interface OwnedTeam {
  id: string
  kind: 'team'
  ownership: string
  name: string
  revision: number
  deleted_at?: string
  definition: {
    name: string
    coordinator?: string
    slots?: { name: string; role: string; min: number; max: number; agent_id?: string }[]
    properties?: { builder_team?: { draft: TeamConfigDraft; preview: Preview }; [key: string]: unknown }
  }
}
export interface TeamPage { items: OwnedTeam[]; next_after_id?: string }
export class TeamSaveError extends Error {
  constructor(message: string, readonly status: number) { super(message) }
}
const BASE = '/api/plugins/console/v1/data/providers/tachyon/verbs/'

/** Owned configuration only. No calls to provider registration or launch APIs. */
export function createOwnedTeamsClient(fetcher: typeof fetch = operatorFetch) {
  const pages = new Map<string, TeamPage>()
  const pending = new Map<string, Promise<TeamPage>>()
  let generation = 0
  async function call<T>(verb: string, input: unknown, write = false): Promise<T> {
    const res = await fetcher(BASE + verb + (write ? '' : '?input=' + encodeURIComponent(JSON.stringify(input))), {
      method: write ? 'POST' : 'GET', cache: 'no-store',
      headers: { Accept: 'application/json', ...(write ? { 'Content-Type': 'application/json' } : {}) },
      ...(write ? { body: JSON.stringify(input) } : {}),
    })
    const value = await res.json()
    if (!res.ok) throw new TeamSaveError(res.status === 409
      ? 'The saved team changed. Your draft is preserved; reload the saved version before editing again.'
      : typeof value.error === 'string' ? value.error : 'Could not save or load the team.', res.status)
    if (value.version !== 1 || value.provider !== 'tachyon' || value.verb !== verb) throw new Error('The owned team service returned an unexpected contract.')
    return value.data as T
  }
  function invalidate() { generation++; pages.clear(); pending.clear() }
  return {
    cached: (after = '') => pages.get(after),
    list(after = ''): Promise<TeamPage> {
      const current = pending.get(after)
      if (current) return current
      const at = generation
      const promise = call<TeamPage>('objects.list', { kind: 'team', limit: 50, after_id: after, include_deleted: true }).then(page => {
        if (at === generation) {
          if (pages.size >= 20) pages.delete(pages.keys().next().value!)
          pages.set(after, page)
        }
        return page
      }).finally(() => { if (pending.get(after) === promise) pending.delete(after) })
      pending.set(after, promise)
      return promise
    },
    get: (id: string) => call<OwnedTeam>('objects.get', { id }),
    async save(draft: TeamConfigDraft, preview: Preview, base?: OwnedTeam) {
      if (base && (base.ownership !== 'tachyon' || base.deleted_at)) throw new Error('Restore an owned team before editing it.')
      const definition = {
        name: draft.name.trim(), coordinator: draft.coordinator,
        slots: draft.slots.map(({ name, role, min, max }) => {
          const agent = base?.definition.slots?.find(slot => slot.name === name)?.agent_id
          return { name, role, min, max, ...(agent ? { agent_id: agent } : {}) }
        }),
        properties: { builder_team: { draft, preview } },
      }
      const result = await call<OwnedTeam>(base ? 'objects.patch' : 'objects.create', base
        ? { id: base.id, expected_revision: base.revision, definition }
        : { id: draft.key, kind: 'team', definition }, true)
      invalidate()
      return result
    },
    async retire(base: OwnedTeam, restore = false) {
      if (base.ownership !== 'tachyon') throw new Error('Provider observations are read-only here.')
      const result = await call<OwnedTeam>(restore ? 'objects.restore' : 'objects.delete', { id: base.id, expected_revision: base.revision }, true)
      invalidate()
      return result
    },
  }
}
export type OwnedTeamsClient = ReturnType<typeof createOwnedTeamsClient>

/** Native fields win over the saved editor draft if another client edits them. */
export function teamDraftFromRecord(row: OwnedTeam): TeamConfigDraft | undefined {
  const saved = row.definition.properties?.builder_team?.draft
  if (!saved || !Array.isArray(saved.slots) || row.ownership !== 'tachyon') return undefined
  return { ...saved, key: row.id, name: row.definition.name, coordinator: row.definition.coordinator ?? '',
    slots: (row.definition.slots ?? []).map(slot => ({ ...slot, launch: saved.slots.find(s => s.name === slot.name)?.launch ?? '' })) }
}

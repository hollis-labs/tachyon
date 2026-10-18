import { expect, it, vi } from 'vitest'
import { createOwnedTeamsClient, teamDraftFromRecord, type OwnedTeam } from './owned-teams'
import { newTeamDraft, teamPreview, type TeamActivationPolicy } from './team-config'

const policy: TeamActivationPolicy = { version: 1, sessionFormation: true, adoption: false, bindings: [{ launch: 'worker', runtime: 'claude', definition: { id: 'worker', revision: 'r1', digest: 'sha256:' + 'a'.repeat(64) } }] }
function fixture() {
  const draft = { ...newTeamDraft(), id: 'my-team', name: 'My team' }
  draft.slots.forEach(s => { s.launch = 'worker' })
  const preview = teamPreview(draft, policy)
  const row: OwnedTeam = { id: draft.key, kind: 'team', ownership: 'tachyon', name: draft.name, revision: 3, definition: {
    name: draft.name, coordinator: draft.coordinator, slots: draft.slots.map(s => ({ ...s, agent_id: 'preserved-agent' })), properties: { unrelated: true, builder_team: { draft, preview } },
  } }
  return { draft, preview, row }
}
function response(verb: string, data: unknown) { return new Response(JSON.stringify({ version: 1, provider: 'tachyon', verb, data })) }

it('deduplicates paged reads and retains a saved page when refresh fails', async () => {
  const { row } = fixture()
  const fetcher = vi.fn<typeof fetch>(async () => response('objects.list', { items: [row], next_after_id: row.id }))
  const api = createOwnedTeamsClient(fetcher)
  const one = api.list(), two = api.list()
  expect(one).toBe(two)
  await one
  expect(api.cached()?.items[0].id).toBe(row.id)
  fetcher.mockRejectedValueOnce(new Error('offline'))
  await expect(api.list()).rejects.toThrow('offline')
  expect(api.cached()?.items[0].id).toBe(row.id)
  await api.list(row.id)
  const args = JSON.parse(new URL(String(fetcher.mock.calls.at(-1)![0]), 'http://localhost').searchParams.get('input')!)
  expect(args).toMatchObject({ kind: 'team', limit: 50, after_id: row.id })
})

it('saves a merge patch pinned to the reviewed revision and preserves native member references', async () => {
  const { draft, preview, row } = fixture()
  const fetcher = vi.fn<typeof fetch>(async () => response('objects.patch', { ...row, revision: 4 }))
  const api = createOwnedTeamsClient(fetcher)
  await api.save({ ...draft, name: 'Edited' }, preview, row)
  const [url, init] = fetcher.mock.calls[0]
  const body = JSON.parse(init!.body as string)
  expect(url).toContain('providers/tachyon/verbs/objects.patch')
  expect(body).toMatchObject({ id: row.id, expected_revision: 3, definition: { name: 'Edited' } })
  expect(body.definition.slots[0].agent_id).toBe('preserved-agent')
  expect(body.definition.properties.builder_team.preview.slots[0].definition_ref).toEqual(policy.bindings[0].definition)
  expect(body.definition.properties).not.toHaveProperty('unrelated') // merge patch leaves unrelated stored properties intact
  expect(row.definition.name).toBe('My team')
})

it('does not retry conflicts or uncertain failures and retains create identity for an explicit retry', async () => {
  const { draft, preview, row } = fixture()
  const fetcher = vi.fn<typeof fetch>(async () => new Response(JSON.stringify({ error: 'conflict' }), { status: 409 }))
  const api = createOwnedTeamsClient(fetcher)
  await expect(api.save(draft, preview, row)).rejects.toMatchObject({ status: 409 })
  expect(fetcher).toHaveBeenCalledTimes(1)
  fetcher.mockRejectedValueOnce(new Error('connection lost'))
  await expect(api.save(draft, preview)).rejects.toThrow('connection lost')
  fetcher.mockResolvedValueOnce(response('objects.create', row))
  await api.save(draft, preview)
  expect(JSON.parse(fetcher.mock.calls[1][1]!.body as string).id).toBe(draft.key)
  expect(fetcher.mock.calls[2][1]!.body).toBe(fetcher.mock.calls[1][1]!.body)
})

it('retires and restores with revision checks and refuses to mutate provider observations', async () => {
  const { row, draft, preview } = fixture()
  const fetcher = vi.fn<typeof fetch>(async (input) => response(String(input).split('/').at(-1)!, row))
  const api = createOwnedTeamsClient(fetcher)
  await api.retire(row)
  await api.retire({ ...row, revision: 4, deleted_at: 'today' }, true)
  expect(fetcher.mock.calls.map(([url, init]) => [String(url).split('/').at(-1), JSON.parse(init!.body as string).expected_revision])).toEqual([['objects.delete', 3], ['objects.restore', 4]])
  await expect(api.save(draft, preview, { ...row, ownership: 'source' })).rejects.toThrow('owned team')
  await expect(api.retire({ ...row, ownership: 'source' })).rejects.toThrow('read-only')
  expect(fetcher).toHaveBeenCalledTimes(2)
  expect(teamDraftFromRecord({ ...row, definition: { ...row.definition, name: 'CLI edit' } })?.name).toBe('CLI edit')
})

import { afterEach, describe, expect, it, vi } from 'vitest'
import { createPlannerId } from './identity'
import { PLANNER_BASE, TachyonPlannerClient } from './client'
import type { PlannerClient } from './contracts'

const id = '019a0000-0000-7000-8000-000000000001'
const target = '019a0000-0000-7000-8000-000000000002'
const json = (value: unknown, status = 200) => new Response(JSON.stringify(value), { status, headers: { 'Content-Type': 'application/json' } })
afterEach(() => vi.unstubAllGlobals())
const ownedDatabases = [{ name: 'ideas', schema: 'tachyon/planner/ideas@1', count: 0, prefix: '', updated: '' }]
function installFetch(fetcher: ReturnType<typeof vi.fn>) {
  vi.stubGlobal('fetch', (url: string, init: RequestInit) => url === `${PLANNER_BASE}/databases` ? Promise.resolve(json(ownedDatabases)) : fetcher(url, init))
}

describe('Tachyon Planner host contract', () => {
  it('resolves a UUID reference through the host without guessing its database', async () => {
    const fetcher = vi.fn().mockResolvedValue(json({ db: 'risks', id: target, item: { id: target, rev: 1 } }))
    installFetch(fetcher)
    expect(await new TachyonPlannerClient().resolveReference(null, target)).toEqual({ db: 'risks', id: target })
    expect(fetcher.mock.calls[0][0]).toBe(`${PLANNER_BASE}/resolve?ref=${target}`)
  })
  it('batches execution references within the host limit and preserves unavailable-source errors', async () => {
    const ids = Array.from({ length: 101 }, (_, n) => `CW-${n}`)
    const fetcher = vi.fn().mockResolvedValueOnce(json({ 'CW-0': null })).mockResolvedValueOnce(json({ error: 'Unavailable', code: 'source_unavailable' }, 502))
    installFetch(fetcher)
    await expect(new TachyonPlannerClient().plannerTaskTitles(ids)).rejects.toMatchObject({ code: 'source_unavailable' })
    expect(new URL(fetcher.mock.calls[0][0], 'http://local').searchParams.get('ids')?.split(',')).toHaveLength(100)
    expect(new URL(fetcher.mock.calls[1][0], 'http://local').searchParams.get('ids')).toBe('CW-100')
  })
  it('keeps reads on the host and carries search/filter values and cancellation', async () => {
    const fetcher = vi.fn().mockImplementation(async () => json([]))
    installFetch(fetcher)
    const api = new TachyonPlannerClient()
    const controller = new AbortController()
    await api.plannerList('ideas', { filters: { status: 'new' }, sort: 'order' }, controller.signal)
    await api.plannerSearch('linked & preserved', 'ideas', controller.signal)
    await api.plannerBoard({ scope: 'all', limit: 30 }, controller.signal)
    await api.plannerSchema('ideas')
    await api.plannerDatabases()
    await api.plannerTaskTitles(['CW-20261018-0066'], controller.signal)
    await api.plannerWorkstreamTasks(id, controller.signal)
    await api.getTask('CW-20261018-0066', controller.signal)
    await api.listTaskComments('CW-20261018-0066', { limit: 5, sort_dir: 'desc' }, controller.signal)
    expect(fetcher.mock.calls.at(-2)?.[0]).toBe(`${PLANNER_BASE}/tasks/CW-20261018-0066`)
    expect(fetcher.mock.calls.at(-1)?.[0]).toBe(`${PLANNER_BASE}/tasks/CW-20261018-0066/comments?limit=5&sort_dir=desc`)
    expect(fetcher.mock.calls.every(([url]) => String(url).startsWith(PLANNER_BASE))).toBe(true)
    expect(new URL(fetcher.mock.calls[1][0], 'http://local').searchParams.get('q')).toBe('linked & preserved')
    expect(fetcher.mock.calls[0][1]).toMatchObject({ credentials: 'same-origin', cache: 'no-store', signal: controller.signal })
  })

  const writes: Array<[string, (api: PlannerClient) => Promise<unknown>, string, string, Record<string, unknown>]> = [
    ['edit', api => api.plannerUpdate('ideas', id, { title: 'Edited' }, 7), `/ideas/${id}`, 'PATCH', { patch: { title: 'Edited' } }],
    ['comment', api => api.plannerComment('ideas', id, 'Retained comment', 7), `/ideas/${id}/comments`, 'POST', { text: 'Retained comment' }],
    ['link', api => api.plannerLinkAdd('ideas', id, { kind: 'torque', ref: 'CW-20261018-0066' }, 7), `/ideas/${id}/links`, 'POST', { link: { kind: 'torque', ref: 'CW-20261018-0066' } }],
    ['unlink', api => api.plannerLinkRemove('ideas', id, 'url', 'https://example.com', 7), `/ideas/${id}/links`, 'DELETE', { kind: 'url', ref: 'https://example.com' }],
    ['decide', api => api.plannerDecide(id, 'option-a', 7, 'Rationale'), `/decisions/${id}/decide`, 'POST', { option: 'option-a', comment: 'Rationale' }],
    ['defer', api => api.plannerDefer(id, 7, 'Later'), `/decisions/${id}/defer`, 'POST', { note: 'Later' }],
    ['reopen', api => api.plannerReopen(id, 7), `/decisions/${id}/reopen`, 'POST', {}],
    ['dismiss', api => api.plannerInboxDismiss(id, 7, 'Duplicate'), `/inbox/${id}/dismiss`, 'POST', { note: 'Duplicate' }],
    ['promote', api => api.plannerInboxPromote(id, 'ideas', 7, target), `/inbox/${id}/promote`, 'POST', { to_db: 'ideas', target_id: target }],
  ]
  it.each(writes)('%s checks the operator-reviewed revision', async (_name, invoke, path, method, body) => {
    const fetcher = vi.fn().mockResolvedValue(json({ id, rev: 8, title: 'Saved' }))
    installFetch(fetcher)
    await invoke(new TachyonPlannerClient())
    expect(fetcher).toHaveBeenCalledTimes(1)
    const [url, init] = fetcher.mock.calls[0]
    expect(url).toBe(PLANNER_BASE + path)
    expect(init.method).toBe(method)
    expect(JSON.parse(init.body)).toEqual({ ...body, expected_revision: 7 })
    expect(init.headers).not.toHaveProperty('Authorization')
  })
  it('sends ordered UUID/revision pairs atomically, not sequential item patches', async () => {
    const fetcher = vi.fn().mockResolvedValue(json([]))
    installFetch(fetcher)
    await new TachyonPlannerClient().plannerReorder('ideas', [{ id: target, rev: 3 }, { id, rev: 7 }])
    expect(fetcher).toHaveBeenCalledTimes(1)
    expect(JSON.parse(fetcher.mock.calls[0][1].body)).toEqual({ items: [{ id: target, expected_revision: 3 }, { id, expected_revision: 7 }] })
  })
  it('retains a caller-owned create UUID across explicit retries', async () => {
    const fetcher = vi.fn().mockRejectedValueOnce(new TypeError('Disconnected')).mockResolvedValueOnce(json({ id, title: 'New', rev: 1 }))
    installFetch(fetcher)
    const api = new TachyonPlannerClient()
    await expect(api.plannerCreate('ideas', { title: 'New' }, id)).rejects.toThrow('Disconnected')
    await api.plannerCreate('ideas', { title: 'New' }, id)
    expect(fetcher.mock.calls[0][1].body).toBe(fetcher.mock.calls[1][1].body)
  })
  it.each([[409, 'conflict'], [401, 'operator_session_required'], [403, 'assurance_insufficient'], [403, 'origin_denied']])('surfaces %s/%s without a write retry or Torque fallback', async (status, code) => {
    const fetcher = vi.fn().mockResolvedValue(json({ code, message: 'Review required', details: { current: { id, rev: 8 } } }, status as number))
    installFetch(fetcher)
    await expect(new TachyonPlannerClient().plannerUpdate('ideas', id, { title: 'Edit' }, 7)).rejects.toMatchObject({ status, code, details: { current: { id, rev: 8 } } })
    expect(fetcher).toHaveBeenCalledTimes(1)
  })
  it('resolves an imported deep link to a UUID before reading and preserves all detail fields', async () => {
    const item = { id, rev: 7, title: 'Imported', notes: 'Original notes', original: { owner: 'Chrispian' }, links: [{ kind: 'torque', ref: 'CW-20261018-0066' }] }
    const fetcher = vi.fn().mockResolvedValueOnce(json(item))
    installFetch(fetcher)
    expect(await new TachyonPlannerClient().plannerGet('ideas', 'ID-004')).toEqual(item)
    expect(fetcher.mock.calls[0][0]).toBe(`${PLANNER_BASE}/ideas/ID-004`)
    expect(fetcher).toHaveBeenCalledTimes(1)
  })
  it('refuses unresolved source IDs and missing revisions before any write', () => {
    const fetcher = vi.fn()
    installFetch(fetcher)
    const api = new TachyonPlannerClient()
    expect(() => api.plannerUpdate('ideas', 'ID-004', {}, 7)).toThrow('Tachyon UUID')
    expect(() => api.plannerUpdate('ideas', id, {}, 0)).toThrow('revision')
    expect(fetcher).not.toHaveBeenCalled()
  })
})

it('creates a valid UUID on HTTP origins without crypto.randomUUID', () => {
  vi.stubGlobal('crypto', { getRandomValues: (bytes: Uint8Array) => bytes.fill(0xab) })
  expect(createPlannerId()).toBe('abababab-abab-4bab-abab-abababababab')
})

it('fails closed when the console proxy still exposes Torque Planner at the owned path', async () => {
  const fetcher = vi.fn().mockResolvedValue(json([{ name: 'ideas', schema: 'portfolio/ideas@1' }]))
  vi.stubGlobal('fetch', fetcher)
  const api = new TachyonPlannerClient()
  await expect(api.plannerCreate('ideas', { title: 'Must not reach Torque' }, id)).rejects.toMatchObject({ code: 'contract_unavailable' })
  expect(fetcher).toHaveBeenCalledTimes(1)
  expect(fetcher.mock.calls[0][0]).toBe(`${PLANNER_BASE}/databases`)
  expect(fetcher.mock.calls[0][1].method).toBeUndefined()
})

it('preserves backend error details from the native error envelope', async () => {
  const fetcher = vi.fn().mockResolvedValue(json({ error: 'Invalid typed reference', code: 'invalid' }, 400))
  installFetch(fetcher)
  await expect(new TachyonPlannerClient().plannerUpdate('ideas', id, {}, 7)).rejects.toMatchObject({ message: 'Invalid typed reference', code: 'invalid' })
})

it('sends pin with a comment only when asked', async () => {
  const fetcher = vi.fn().mockImplementation(async () => json({ id, rev: 3 }))
  installFetch(fetcher)
  const api = new TachyonPlannerClient()
  await api.plannerComment('themes', id, 'plain', 2)
  await api.plannerComment('themes', id, 'pinned', 2, undefined, { pin: true })
  expect(JSON.parse(fetcher.mock.calls[0][1].body)).not.toHaveProperty('pin')
  expect(JSON.parse(fetcher.mock.calls[1][1].body)).toMatchObject({ text: 'pinned', pin: true, expected_revision: 2 })
})

it('writes a typed relation on the source item and removes it with the same body', async () => {
  const fetcher = vi.fn().mockImplementation(async () => json({ item: { id, db: 'themes', revision: 9 }, links_view: { outbound: [], inbound: [], external: [] } }))
  installFetch(fetcher)
  const api = new TachyonPlannerClient()
  await api.plannerRelationAdd('themes', 'theme-a', 'needs', { db: 'themes', id: 'theme-b' }, 8)
  await api.plannerRelationRemove('themes', 'theme-a', 'needs', { db: 'themes', id: 'theme-b' }, 9)
  expect(fetcher.mock.calls[0][0]).toBe(`${PLANNER_BASE}/themes/theme-a/relations`)
  expect(fetcher.mock.calls[0][1].method).toBe('POST')
  expect(JSON.parse(fetcher.mock.calls[0][1].body)).toEqual({ kind: 'needs', to: { db: 'themes', id: 'theme-b' }, expected_revision: 8 })
  expect(fetcher.mock.calls[1][1].method).toBe('DELETE')
})

it('reads the links view and surfaces a cycle as its own code', async () => {
  const fetcher = vi.fn().mockResolvedValueOnce(json({ outbound: [], inbound: [], external: [] })).mockResolvedValueOnce(json({ error: 'That link would make a cycle.', code: 'cycle' }, 422))
  installFetch(fetcher)
  const api = new TachyonPlannerClient()
  await api.plannerLinks('themes', 'theme-a')
  expect(fetcher.mock.calls[0][0]).toBe(`${PLANNER_BASE}/themes/theme-a/links`)
  await expect(api.plannerRelationAdd('themes', 'theme-a', 'needs', { db: 'themes', id: 'theme-b' }, 2)).rejects.toMatchObject({ status: 422, code: 'cycle' })
})

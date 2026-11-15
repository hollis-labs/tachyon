import { operatorFetch } from '../operator/elevation'

export type BuilderKind = 'agent' | 'assignment'
export interface ObjectRecord {
  id: string; kind: string; ownership: string; name: string; revision: number
  definition: Record<string, unknown>
  deleted_at?: string
  source?: { key: { provider: string; instance: string; external_id: string }; deleted_at?: string }
}
export interface ObjectPage { items: ObjectRecord[]; next_after_id?: string }
export interface HistoryPage { items: { record: ObjectRecord; operation: string; actor: string; recorded_at: string }[]; next_after_revision?: number }
export const isRetired = (row: ObjectRecord) => !!row.deleted_at || row.ownership === 'source' && !!row.source?.deleted_at
export class OwnedBuilderError extends Error {
  constructor(message: string, readonly status: number) { super(message) }
}
const BASE = '/api/plugins/console/v1/data/providers/tachyon/verbs/'

export function createOwnedBuilderClient(fetcher: typeof fetch = operatorFetch) {
  const cache = new Map<string, ObjectPage>()
  const pending = new Map<string, Promise<ObjectPage>>()
  let generation = 0
  const key = (kind: string, after = '') => JSON.stringify([kind, after])
  async function call<T>(verb: string, input: unknown, write = false): Promise<T> {
    const res = await fetcher(BASE + verb + (write ? '' : '?input=' + encodeURIComponent(JSON.stringify(input))), {
      method: write ? 'POST' : 'GET', cache: 'no-store',
      headers: { Accept: 'application/json', ...(write ? { 'Content-Type': 'application/json' } : {}) },
      ...(write ? { body: JSON.stringify(input) } : {}),
    })
    const body = await res.json()
    if (!res.ok) throw new OwnedBuilderError(res.status === 409
      ? 'This record changed since you opened it. Your draft is preserved; reload before reconciling your changes.'
      : typeof body.error === 'string' ? body.error : 'The data service could not complete the request.', res.status)
    if (body.version !== 1 || body.provider !== 'tachyon' || body.verb !== verb) throw new Error('Unexpected owned data contract.')
    return body.data as T
  }
  function committed(row: ObjectRecord) {
    generation++
    pending.clear()
    for (const [k, page] of cache) {
      if (JSON.parse(k)[0] !== row.kind) continue
      cache.set(k, { ...page, items: page.items.map(item => item.id === row.id ? row : item) })
    }
    return row
  }
  function editable(row: ObjectRecord) {
    if (row.ownership !== 'tachyon' || isRetired(row)) throw new Error('Adopt or restore the definition before editing it.')
  }
  return {
    cached: (kind: string, after = '') => cache.get(key(kind, after)),
    list(kind: string, after = '') {
      const k = key(kind, after)
      const existing = pending.get(k)
      if (existing) return existing
      const at = generation
      const request = call<ObjectPage>('objects.list', { kind, after_id: after, limit: 50, include_deleted: true }).then(page => {
        if (at === generation) {
          if (cache.size >= 60) cache.delete(cache.keys().next().value!)
          cache.set(k, page)
        }
        return page
      }).finally(() => { if (pending.get(k) === request) pending.delete(k) })
      pending.set(k, request)
      return request
    },
    get: (id: string) => call<ObjectRecord>('objects.get', { id }),
    history: (id: string, after = 0) => call<HistoryPage>('objects.history', { id, after_revision: after, limit: 25 }),
    async save(kind: BuilderKind, id: string, definition: Record<string, unknown>, base?: ObjectRecord) {
      if (base) { editable(base); if (base.id !== id || base.kind !== kind) throw new Error('The reviewed record does not match this draft.') }
      return committed(await call<ObjectRecord>(base ? 'objects.patch' : 'objects.create', base
        ? { id, expected_revision: base.revision, definition }
        : { id, kind, definition }, true))
    },
    async lifecycle(row: ObjectRecord, action: 'delete' | 'restore' | 'take_ownership') {
      if (action === 'take_ownership' ? row.ownership !== 'source' || isRetired(row) : row.ownership !== 'tachyon') throw new Error('This action is not available for this record.')
      return committed(await call<ObjectRecord>('objects.' + action, { id: row.id, expected_revision: row.revision }, true))
    },
  }
}
export type OwnedBuilderClient = ReturnType<typeof createOwnedBuilderClient>
export const ownedBuilderClient = createOwnedBuilderClient()

export function newObjectID() {
  const bytes = crypto.getRandomValues(new Uint8Array(16))
  bytes[6] = (bytes[6] & 15) | 64; bytes[8] = (bytes[8] & 63) | 128
  const s = Array.from(bytes, b => b.toString(16).padStart(2, '0')).join('')
  return `${s.slice(0, 8)}-${s.slice(8, 12)}-${s.slice(12, 16)}-${s.slice(16, 20)}-${s.slice(20)}`
}

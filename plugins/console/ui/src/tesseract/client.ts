import { hostFetch } from '@tachyon/host-sdk'
import { parseTesseractKey } from '../refs'

const BASE = '/api/plugins/console/v1/tesseract'
const KEY_DOMAINS = 'memory,knowledge,workspace'

/** The fields of a Tesseract item the console reads. The server sends more; none of it is needed here. */
export interface TesseractItem {
  item_id: string
  domain: string
  namespace: string
  key: string
  version_token?: string
  tags?: string[]
  payload?: { summary?: string; body?: string; title?: string; [k: string]: unknown }
  author?: { agent_id?: string }
  created_at?: string
  updated_at?: string
}

export interface TesseractReadClient {
  /** The item, or null when Tesseract has no such id. Any other failure throws. */
  getItem(id: string, signal?: AbortSignal): Promise<TesseractItem | null>
  /** The newest item with exactly this `[domain@]namespace#key`, or null. */
  findByKey(canonical: string, signal?: AbortSignal): Promise<TesseractItem | null>
}

async function getJson<T>(path: string, signal?: AbortSignal): Promise<T | null> {
  const res = await hostFetch(`${BASE}${path}`, { headers: { Accept: 'application/json' }, cache: 'no-store', signal })
  if (res.status === 404) return null
  if (!res.ok) {
    const body = await res.json().catch(() => null)
    throw new Error(body?.error?.message ?? body?.message ?? `Tesseract answered ${res.status}`)
  }
  return (await res.json()) as T
}

export function createTesseractReadClient(): TesseractReadClient {
  const getItem: TesseractReadClient['getItem'] = (id, signal) => getJson<TesseractItem>(`/items/${encodeURIComponent(id)}`, signal)
  return {
    getItem,
    async findByKey(canonical, signal) {
      const k = parseTesseractKey(canonical)
      if (!k) return null
      const q = new URLSearchParams({ namespace: k.namespace, domains: k.domain ?? KEY_DOMAINS, format: 'brief', limit: '500' })
      const page = await getJson<{ results?: { item_id: string; key: string; updated_at?: string }[] }>(`/recall?${q}`, signal)
      const hits = (page?.results ?? []).filter((r) => r.key === k.key)
      if (hits.length === 0) return null
      hits.sort((a, b) => (b.updated_at ?? '').localeCompare(a.updated_at ?? ''))
      return getItem(hits[0].item_id, signal)
    },
  }
}

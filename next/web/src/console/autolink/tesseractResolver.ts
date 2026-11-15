import type { KindResolver, RefEntity, ResolveOutcome, StatusTone } from '../refs'
import type { TesseractItem } from '../tesseract/client'
import type { TesseractCache } from './tesseractDetail'

const DOMAIN_TONE: Record<string, StatusTone> = { workspace: 'info', knowledge: 'success', memory: 'neutral', event: 'neutral' }

/** Tags that name what an item is, in the order they win. */
const KNOWN_TYPES = ['handoff', 'decision', 'adr', 'investigation', 'followup', 'limitation', 'draft', 'state'] as const

/** What kind of record this is: a known type tag, else the namespace's last segment, else the domain. */
export function tesseractItemType(item: TesseractItem): string {
  const tag = KNOWN_TYPES.find((t) => item.tags?.includes(t))
  if (tag) return tag
  const seg = item.namespace.split('/').pop()
  return seg && seg !== item.domain ? seg : item.domain
}

export function tesseractItemTitle(item: TesseractItem): string {
  const t = item.payload?.title
  return typeof t === 'string' && t.trim() ? t : item.key
}

/** The facts a chip or hovercard header needs. Bodies are fetched by the panes, from the same cache. */
export function tesseractEntity(item: TesseractItem): RefEntity {
  const type = tesseractItemType(item)
  return {
    title: tesseractItemTitle(item),
    chipLabel: `${type.charAt(0).toUpperCase()}${type.slice(1)}: ${tesseractItemTitle(item)}`,
    subtitle: item.namespace,
    description: item.payload?.summary,
    status: { label: item.domain, tone: DOMAIN_TONE[item.domain] ?? 'neutral' },
    fields: [
      { label: 'Type', value: type },
      { label: 'ID', value: item.item_id, mono: true },
    ],
    updatedAt: item.updated_at,
  }
}

/** Resolver for `tesseract.item` and `tesseract.key`: one cache read per reference, shared with the drawer. */
export function createTesseractResolver(kind: string, cache: TesseractCache): KindResolver {
  return {
    async resolveMany(canonicals) {
      const out: Record<string, ResolveOutcome> = {}
      await Promise.all(
        canonicals.map(async (c) => {
          const entry = await cache.ensure(kind, c)
          if (entry.phase === 'error' && !entry.item) throw new Error(entry.error ?? 'Failed to read Tesseract')
          out[c] = entry.item ? { status: 'found', entity: tesseractEntity(entry.item) } : { status: 'not-found' }
        }),
      )
      return out
    },
  }
}

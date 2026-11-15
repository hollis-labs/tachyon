import { isWordChar, matches } from '../scan-utils'
import type { RefKind } from '../types'

// caster://ns/name or caster://ns/name@N. No query, fragment or extra segments.
const CASTER_RE = /caster:\/\/[A-Za-z0-9._-]{1,64}\/[A-Za-z0-9._-]{1,128}(?:@[1-9]\d{0,5})?/g

export function createCasterKind(): RefKind {
  return {
    id: 'caster',
    label: 'Caster artifact',
    icon: 'file',
    priority: 95,
    scan(text) {
      const hits = []
      for (const m of matches(CASTER_RE, text)) {
        const start = m.index
        if (isWordChar(text[start - 1])) continue
        hits.push({ start, end: start + m[0].length, canonical: m[0] })
      }
      return hits
    },
    chipLabel: (ref) => ref.canonical.replace('caster://', ''),
    open: (ref) => ({ type: 'copy', text: ref.canonical }),
  }
}

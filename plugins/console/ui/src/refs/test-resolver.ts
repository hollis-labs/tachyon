import type { KindResolver, RefEntity, ResolveOutcome } from './types'

// Test-only resolver backed by an inline map (never used at runtime).
export interface MockResolverOptions {
  latencyMs?: number
  /** Canonicals that make the whole batch throw. */
  failFor?: readonly string[]
  /** Inspect calls in tests: each entry is one resolveMany batch. */
  onCall?: (canonicals: string[]) => void
}

/** A KindResolver backed by a fixture map; anything not in it is not-found. */
export function createMockResolver(fixtures: Record<string, RefEntity>, opts: MockResolverOptions = {}): KindResolver {
  return {
    async resolveMany(canonicals, { signal }) {
      opts.onCall?.([...canonicals])
      if (opts.latencyMs) {
        await new Promise<void>((resolve, reject) => {
          const t = setTimeout(resolve, opts.latencyMs)
          signal.addEventListener('abort', () => {
            clearTimeout(t)
            reject(new DOMException('aborted', 'AbortError'))
          })
        })
      }
      if (opts.failFor && canonicals.some((c) => opts.failFor!.includes(c))) throw new Error('mock resolver failure')
      const out: Record<string, ResolveOutcome> = {}
      for (const c of canonicals) out[c] = fixtures[c] ? { status: 'found', entity: fixtures[c] } : { status: 'not-found' }
      return out
    },
  }
}

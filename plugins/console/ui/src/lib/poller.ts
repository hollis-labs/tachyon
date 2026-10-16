// Poll-based push for list endpoints that have no relayed stream yet. Emits
// only items whose JSON changed since the last poll. Backs off on errors
// (missing endpoint) up to `maxMs`, so a GAP backend costs one request per
// few minutes instead of a retry storm.
export interface PollerOptions<T> {
  load: () => Promise<T[]>
  key: (item: T) => string
  intervalMs?: number
  maxMs?: number
}

export function createListPoller<T>(opts: PollerOptions<T>) {
  const listeners = new Set<(changed: T[]) => void>()
  const seen = new Map<string, string>()
  const base = opts.intervalMs ?? 15_000
  const max = opts.maxMs ?? 300_000
  let delay = base
  let timer: ReturnType<typeof setTimeout> | null = null

  const tick = async () => {
    timer = null
    try {
      const items = await opts.load()
      delay = base
      const changed = items.filter((it) => {
        const k = opts.key(it)
        const json = JSON.stringify(it)
        if (seen.get(k) === json) return false
        seen.set(k, json)
        return true
      })
      if (changed.length) listeners.forEach((l) => l(changed))
    } catch {
      delay = Math.min(delay * 2, max)
    }
    if (listeners.size && !timer) timer = setTimeout(tick, delay)
  }

  return {
    /** Local echo of a write result. */
    emit(items: T[]) {
      for (const it of items) seen.set(opts.key(it), JSON.stringify(it))
      if (items.length) listeners.forEach((l) => l(items))
    },
    subscribe(listener: (changed: T[]) => void): () => void {
      listeners.add(listener)
      if (!timer) timer = setTimeout(tick, delay)
      return () => {
        listeners.delete(listener)
        if (!listeners.size && timer) {
          clearTimeout(timer)
          timer = null
        }
      }
    },
  }
}

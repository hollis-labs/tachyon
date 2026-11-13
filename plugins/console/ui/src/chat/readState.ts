import { consoleJson } from '../lib/http'

// Marking read is a write, but it must never raise the OPERATOR ACCESS dialog (it fires on every open). Without operator
// access the server answers 401/403: stop asking for a while instead of failing once per session on every thread.
let blockedUntil = 0
const BLOCK_MS = 5 * 60_000

// Opening a chat marks its session from more than one path at once (history load, live sync, sibling
// summaries): share one request per session while it is in flight instead of posting it again.
const inflight = new Map<string, Promise<void>>()

export function markReadQuiet(ref: string): Promise<void> {
  if (Date.now() < blockedUntil) return Promise.resolve()
  const running = inflight.get(ref)
  if (running) return running
  const p = (async () => {
    try {
      await consoleJson(`/chat/threads/${encodeURIComponent(ref)}/read`, { method: 'POST' })
    } catch (e) {
      const status = (e as { status?: number }).status
      if (status === 401 || status === 403) blockedUntil = Date.now() + BLOCK_MS
    } finally {
      inflight.delete(ref)
    }
  })()
  inflight.set(ref, p)
  return p
}

export const resetReadStateForTest = () => { blockedUntil = 0; inflight.clear() }

import { consoleJson } from '../lib/http'

// Marking read is a write, but it must never raise the OPERATOR ACCESS dialog (it fires on every open). Without operator
// access the server answers 401/403: stop asking for a while instead of failing once per session on every thread.
let blockedUntil = 0
const BLOCK_MS = 5 * 60_000

export async function markReadQuiet(ref: string): Promise<void> {
  if (Date.now() < blockedUntil) return
  try {
    await consoleJson(`/chat/threads/${encodeURIComponent(ref)}/read`, { method: 'POST' })
  } catch (e) {
    const status = (e as { status?: number }).status
    if (status === 401 || status === 403) blockedUntil = Date.now() + BLOCK_MS
  }
}

export const resetReadStateForTest = () => { blockedUntil = 0 }

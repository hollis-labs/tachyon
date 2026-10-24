// Unsent composer text, kept per thread so switching conversations (or the dock and
// the page) does not lose it. In memory, mirrored to sessionStorage for reloads.

const KEY = 'tachyon.chat.drafts'
let drafts: Map<string, string> | null = null

function load(): Map<string, string> {
  if (drafts) return drafts
  drafts = new Map()
  try {
    const raw = sessionStorage.getItem(KEY)
    if (raw) for (const [k, v] of Object.entries(JSON.parse(raw) as Record<string, unknown>)) if (typeof v === 'string') drafts.set(k, v)
  } catch { /* storage is optional */ }
  return drafts
}

export function getDraft(threadId: string): string {
  return load().get(threadId) ?? ''
}

export function setDraft(threadId: string, text: string): void {
  const m = load()
  if (text) m.set(threadId, text)
  else m.delete(threadId)
  try { sessionStorage.setItem(KEY, JSON.stringify(Object.fromEntries(m))) } catch { /* storage is optional */ }
}

/** Test helper: forget everything held in memory. */
export function resetDraftsForTest(): void {
  drafts = null
  try { sessionStorage.removeItem(KEY) } catch { /* storage is optional */ }
}

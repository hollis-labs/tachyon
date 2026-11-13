import { useCallback, useEffect, useState } from 'react'
import { chatJSON } from '../roster'

const JSON_HEADERS = { 'Content-Type': 'application/json' }

/** Only Tether sessions have a lineage a replacement session can follow. */
export const continuable = (ref: string | undefined): ref is string => !!ref && ref.startsWith('tether:')

export interface ContinueState {
  /** The session's Continue flag; null while loading, for a thread that is not a Tether session, or when it cannot be read. */
  on: boolean | null
  busy: boolean
  /** Why the last change failed; the flag is back to its previous value. */
  error: string
  toggle: () => void
}

/**
 * The session's Continue toggle (chat API `/threads/{ref}/continue`). On: its plan follows to the
 * session that continues it. Off: the replacement starts fresh. The server stores it per session
 * and fixes a new session's value from the "Default for Continue on new sessions" setting the
 * first time it is read, so changing that setting never flips a session already open.
 */
export function useContinue(ref: string | undefined): ContinueState {
  const [on, setOn] = useState<boolean | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  useEffect(() => {
    setOn(null)
    setError('')
    if (!continuable(ref)) return
    let live = true
    chatJSON<{ continue: boolean }>(`/threads/${encodeURIComponent(ref)}/continue`).then(
      (r) => { if (live) setOn(r.continue) },
      () => undefined, // a session the chat API cannot answer for has no toggle
    )
    return () => { live = false }
  }, [ref])
  const toggle = useCallback(() => {
    if (!continuable(ref) || on === null || busy) return
    const next = !on
    setOn(next)
    setBusy(true)
    chatJSON<{ continue: boolean }>(`/threads/${encodeURIComponent(ref)}/continue`, { method: 'PUT', headers: JSON_HEADERS, body: JSON.stringify({ continue: next }) }).then(
      (r) => { setOn(r.continue); setError('') },
      (e) => { setOn(!next); setError(e instanceof Error ? e.message : 'Could not change Continue.') },
    ).finally(() => setBusy(false))
  }, [ref, on, busy])
  return { on, busy, error, toggle }
}

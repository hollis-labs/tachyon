import { useCallback, useEffect, useRef, useState } from 'react'
import type { CardPhase } from './types'

/** Undo window for a reversible answer; the resolve POST is deferred this long. */
export const UNDO_MS = 8000

/**
 * One card's answer lifecycle: pending -> (answered, Undo for 8 s when reversible, then committed) | saving -> answered.
 * A failed commit returns the card to answerable with the error shown; there is never a double submit.
 */
export function useAnswer<V>({ reversible, commit, initial }: { reversible: boolean; commit?: (v: V) => Promise<void>; initial?: V }) {
  const [phase, setPhase] = useState<CardPhase>(initial !== undefined ? 'answered' : 'pending')
  const [value, setValue] = useState<V | undefined>(initial)
  const [error, setError] = useState('')
  const [undoLeft, setUndoLeft] = useState(0)
  const timer = useRef<ReturnType<typeof setInterval> | null>(null)
  const pending = useRef<V | null>(null)
  const clear = () => { if (timer.current) clearInterval(timer.current); timer.current = null }
  useEffect(() => clear, [])

  const run = useCallback(async (v: V) => {
    setError('')
    try { await commit?.(v) }
    catch (e) { setPhase('pending'); setValue(undefined); setError(e instanceof Error ? e.message : 'Could not save the answer.'); return }
    setPhase('answered')
  }, [commit])

  const answer = useCallback((v: V) => {
    if (phase !== 'pending') return
    setValue(v)
    if (!reversible) { setPhase('saving'); void run(v); return }
    pending.current = v
    setPhase('answered')
    setUndoLeft(UNDO_MS / 1000)
    const end = Date.now() + UNDO_MS
    timer.current = setInterval(() => {
      const left = Math.ceil((end - Date.now()) / 1000)
      if (left > 0) { setUndoLeft(left); return }
      clear(); setUndoLeft(0)
      const p = pending.current; pending.current = null
      if (p !== null) void run(p)
    }, 250)
  }, [phase, reversible, run])

  const undo = useCallback(() => {
    clear(); pending.current = null; setUndoLeft(0); setPhase('pending'); setValue(undefined)
  }, [])

  return { phase, value, error, undoLeft, answer, undo }
}

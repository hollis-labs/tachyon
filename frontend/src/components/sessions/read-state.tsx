import { useCallback, useEffect, useRef, useState } from "react"
import type { AskDetail, Envelope } from "../../api/verbs"
import { PendingApproval } from "../pending-approval"

export function useSessionRead<T>(load: () => Promise<Envelope<T>>, enabled: boolean) {
  const [result, setResult] = useState<Envelope<T> | null>(null)
  const [busy, setBusy] = useState(false)
  const flight = useRef<Promise<void> | null>(null)
  const generation = useRef(0)
  const refresh = useCallback(async () => {
    if (flight.current) return flight.current
    if (!enabled) return
    const version = generation.current
    setBusy(true)
    const pending = load()
      .then((next) => {
        if (generation.current === version) setResult(next)
      })
      .catch(() => {
        if (generation.current === version)
          setResult({
            status: "error",
            error: { code: "transport", message: "Session read failed." },
          })
      })
      .finally(() => {
        if (generation.current === version) setBusy(false)
        if (flight.current === pending) flight.current = null
      })
    flight.current = pending
    return pending
  }, [load, enabled])
  useEffect(() => {
    generation.current++
    flight.current = null
    setResult(null)
    setBusy(false)
    void refresh()
    return () => {
      generation.current++
    }
  }, [refresh])
  return { result, busy, refresh }
}
export function SessionReadFeedback({
  busy,
  result,
}: {
  busy: boolean
  result: Envelope<unknown> | null
}) {
  if (busy) return <p role="status">Loading session data…</p>
  if (result?.status === "ask") return <PendingApproval ask={result.ask} />
  if (result?.status === "error")
    return (
      <p role="alert" className="break-words text-sm text-status-failed">
        {result.error.message}
      </p>
    )
  return null
}
export interface MutationFeedback {
  busy: boolean
  ask?: AskDetail
  message: string
}

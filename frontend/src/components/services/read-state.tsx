import { Button, Skeleton } from "@hollis-labs/design-components"
import { useCallback, useEffect, useRef, useState } from "react"
import type { Envelope } from "../../api/verbs"

export function useServiceRead<T>(enabled: boolean, request: () => Promise<Envelope<T>>) {
  const [result, setResult] = useState<Envelope<T> | null>(null)
  const [data, setData] = useState<T | null>(null)
  const [pending, setPending] = useState(false)
  const [slow, setSlow] = useState(false)
  const [updated, setUpdated] = useState<Date | null>(null)
  const mounted = useRef(false)
  const flight = useRef(false)
  const refresh = useCallback(async () => {
    if (!enabled || flight.current) return
    flight.current = true
    setPending(true)
    setSlow(false)
    // service_health takes up to ~90s (three serial 30s reads). Polling would
    // starve other calls on the serial plugin pipe. This timer only reveals a hint.
    const timer = setTimeout(() => mounted.current && setSlow(true), 3000)
    try {
      const next = await request()
      if (mounted.current) {
        setResult(next)
        if (next.status === "ok") {
          setData(next.data)
          setUpdated(new Date())
        }
      }
    } catch (failure) {
      if (mounted.current)
        setResult({
          status: "error",
          error: {
            code: "network_error",
            message: failure instanceof Error ? failure.message : "Read failed",
          },
        })
    } finally {
      clearTimeout(timer)
      flight.current = false
      if (mounted.current) {
        setPending(false)
        setSlow(false)
      }
    }
  }, [enabled, request])
  useEffect(() => {
    mounted.current = true
    void refresh()
    return () => {
      mounted.current = false
    }
  }, [refresh])
  return { result, data, pending, slow, updated, refresh }
}

export function ReadFeedback<T>({
  state,
  health = false,
}: {
  state: ReturnType<typeof useServiceRead<T>>
  health?: boolean
}) {
  const { result, pending, slow, updated } = state
  return (
    <div className="grid gap-2 text-sm" aria-live="polite">
      {pending && (
        <p role="status">
          {health ? "Checking…" : "Loading…"}
          {health && slow ? " can take up to ~90s" : ""}
        </p>
      )}
      {pending && !result && <Skeleton className="h-12 w-full" />}
      {!pending && result?.status === "error" && (
        <p role="alert">
          {result.error.code}: {result.error.message}
          {health ? " · Current health unknown." : ""}
        </p>
      )}
      {!pending && result?.status === "ask" && (
        <div role="status">
          <p>Operator decision required: {result.ask.prompt}</p>
          {result.ask.options?.length ? <p>Options: {result.ask.options.join(" / ")}</p> : null}
          <p>No approval is submitted from this read-only page.</p>
        </div>
      )}
      {updated && (
        <p className="text-text-muted">
          Last refreshed: {updated.toLocaleString()}
          {pending || result?.status !== "ok" ? " (previous successful check)" : ""}
        </p>
      )}
    </div>
  )
}

export function RefreshButton<T>({ state }: { state: ReturnType<typeof useServiceRead<T>> }) {
  return (
    <Button variant="outline" disabled={state.pending} onClick={() => void state.refresh()}>
      Refresh
    </Button>
  )
}

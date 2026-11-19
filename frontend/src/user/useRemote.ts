import { useCallback, useEffect, useState } from "react"

export type Remote<T> =
  | { status: "loading" }
  | { status: "error"; message: string }
  | { status: "ready"; data: T }

/** One card's data. A failure stays inside that card; reload retries it. */
export function useRemote<T>(load: () => Promise<T>): [Remote<T>, () => void] {
  const [state, setState] = useState<Remote<T>>({ status: "loading" })
  const [tick, setTick] = useState(0)
  // biome-ignore lint/correctness/useExhaustiveDependencies: load is a stable module function; tick re-runs it
  useEffect(() => {
    let live = true
    setState({ status: "loading" })
    load().then(
      (data) => live && setState({ status: "ready", data }),
      (e) =>
        live && setState({ status: "error", message: e instanceof Error ? e.message : "Failed" }),
    )
    return () => {
      live = false
    }
  }, [tick])
  return [state, useCallback(() => setTick((n) => n + 1), [])]
}

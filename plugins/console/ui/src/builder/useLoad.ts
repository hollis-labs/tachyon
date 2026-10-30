import { useCallback, useEffect, useState } from 'react'
import { errorMessage } from './api'

export type Load<T> = { status: 'loading' } | { status: 'error'; message: string } | { status: 'ready'; data: T }

/** One pane's data. Failure stays in the pane; the returned function retries. */
export function useLoad<T>(load: () => Promise<T>, deps: readonly unknown[]): [Load<T>, () => void] {
  const [state, setState] = useState<Load<T>>({ status: 'loading' })
  const [tick, setTick] = useState(0)
  // biome-ignore lint/correctness/useExhaustiveDependencies: caller supplies deps; tick retries
  useEffect(() => {
    let live = true
    setState({ status: 'loading' })
    load().then(
      (data) => live && setState({ status: 'ready', data }),
      (e) => live && setState({ status: 'error', message: errorMessage(e) }),
    )
    return () => {
      live = false
    }
  }, [...deps, tick])
  return [state, useCallback(() => setTick((n) => n + 1), [])]
}

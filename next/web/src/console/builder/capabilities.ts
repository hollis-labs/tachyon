import { useEffect, useState } from 'react'
import { type Capabilities, builderApi } from './api'

export type ChangeMode = Capabilities['changes']['mode']

/** How edits are applied here: direct, handed off to Tether, or not at all. Unknown until read; reads again on demand. */
export function useChangeMode(): [ChangeMode | undefined, () => void] {
  const [mode, setMode] = useState<ChangeMode>()
  const [n, setN] = useState(0)
  useEffect(() => {
    let live = true
    builderApi.capabilities().then(
      (c) => live && setMode(c.changes.mode),
      () => live && setMode(undefined),
    )
    return () => {
      live = false
    }
  }, [n])
  return [mode, () => setN((x) => x + 1)]
}

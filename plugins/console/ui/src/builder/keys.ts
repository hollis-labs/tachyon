import { useEffect, useRef } from 'react'

const inField = (t: EventTarget | null) => t instanceof HTMLElement && (t.isContentEditable || ['INPUT', 'TEXTAREA', 'SELECT'].includes(t.tagName))
/** A dialog, menu or listbox is open: page-level keys stand down. */
export const overlayOpen = () => document.querySelector('[role="dialog"],[role="alertdialog"],[role="menu"],[role="listbox"]') !== null

/** Single-key page shortcuts (j, k, [, ]). Inactive in fields, with a modifier held, or while an overlay is open. */
export function useKeys(map: Record<string, () => void>) {
  const latest = useRef(map)
  latest.current = map
  useEffect(() => {
    const on = (e: KeyboardEvent) => {
      if (e.defaultPrevented || e.metaKey || e.ctrlKey || e.altKey || inField(e.target) || overlayOpen()) return
      const run = latest.current[e.key]
      if (!run) return
      e.preventDefault()
      run()
    }
    window.addEventListener('keydown', on)
    return () => window.removeEventListener('keydown', on)
  }, [])
}

/** The id before or after `current` in `ids`; the first (or last) when nothing is current. */
export function stepId(ids: string[], current: string | undefined, by: 1 | -1): string | undefined {
  if (ids.length === 0) return undefined
  const i = current === undefined ? -1 : ids.indexOf(current)
  if (i === -1) return by === 1 ? ids[0] : ids[ids.length - 1]
  return ids[Math.min(ids.length - 1, Math.max(0, i + by))]
}

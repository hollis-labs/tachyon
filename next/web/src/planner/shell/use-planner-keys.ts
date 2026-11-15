import { useEffect, useRef } from 'react'

export interface PlannerKeyHandlers {
  palette: () => void
  create: () => void
  link: () => void
  lens: (letter: string) => void
  view: (v: 'board' | 'timeline') => void
  pin: () => void
  finish: () => void
  step: (delta: 1 | -1) => void
  clearFilter: () => boolean
}

const typing = (t: EventTarget | null) => {
  const el = t as HTMLElement | null
  return !!el && (el.isContentEditable || ['INPUT', 'TEXTAREA', 'SELECT'].includes(el.tagName))
}

/** The page-level keyboard map. Ignored while typing or while an overlay owns the keys. */
export function usePlannerKeys(h: PlannerKeyHandlers, enabled: boolean) {
  const ref = useRef(h)
  ref.current = h
  const pendingG = useRef(0)
  useEffect(() => {
    if (!enabled) return
    const onKey = (e: KeyboardEvent) => {
      if (e.metaKey || e.ctrlKey || e.altKey || typing(e.target)) return
      if (document.querySelector('[role="dialog"], [role="menu"]')) return
      const k = e.key.toLowerCase()
      // The spine list handles J/K/P itself while it has focus.
      if ((k === 'j' || k === 'k' || k === 'p') && (e.target as HTMLElement | null)?.closest?.('[role="listbox"]')) return
      const now = Date.now()
      if (pendingG.current && now - pendingG.current < 1500) {
        pendingG.current = 0
        e.preventDefault()
        return ref.current.lens(k)
      }
      pendingG.current = 0
      if (k === 'g') pendingG.current = now
      else if (k === '/') (e.preventDefault(), ref.current.palette())
      else if (k === 'n') (e.preventDefault(), ref.current.create())
      else if (k === 'l') (e.preventDefault(), ref.current.link())
      else if (k === 'b') ref.current.view('board')
      else if (k === 'm') ref.current.view('timeline')
      else if (k === 'p') ref.current.pin()
      else if (k === 'f') ref.current.finish()
      else if (k === 'escape') ref.current.clearFilter()
      else if (k === 'j' || k === 'k') ref.current.step(k === 'j' ? 1 : -1)
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [enabled])
}

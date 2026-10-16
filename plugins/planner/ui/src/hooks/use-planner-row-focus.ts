import { useEffect, useState } from 'react'

/** Preserve focus restoration, without treating a pointer-opened row as selected. */
export function usePlannerRowFocus() {
  const [mode, setMode] = useState<'pointer' | 'keyboard'>('keyboard')

  useEffect(() => {
    const pointer = () => setMode('pointer')
    const keyboard = (event: KeyboardEvent) => {
      // Escape inside a dialog restores focus; it is not navigation on the list.
      if (event.target instanceof Element && event.target.closest('[role="dialog"], [role="alertdialog"]')) return
      if (['Tab', 'Enter', ' ', 'ArrowUp', 'ArrowDown', 'ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(event.key)) {
        setMode('keyboard')
      }
    }
    document.addEventListener('pointerdown', pointer, true)
    document.addEventListener('keydown', keyboard, true)
    return () => {
      document.removeEventListener('pointerdown', pointer, true)
      document.removeEventListener('keydown', keyboard, true)
    }
  }, [])

  return mode
}

import { useEffect, useState } from 'react'

const QUERY = '(max-width: 1023px)'

/** True below the kit's lg breakpoint, where side columns become overlays. */
export function useNarrow(): boolean {
  const [narrow, setNarrow] = useState(() => (typeof window.matchMedia === 'function' ? window.matchMedia(QUERY).matches : false))
  useEffect(() => {
    if (typeof window.matchMedia !== 'function') return
    const mq = window.matchMedia(QUERY)
    const on = () => setNarrow(mq.matches)
    on()
    mq.addEventListener('change', on)
    return () => mq.removeEventListener('change', on)
  }, [])
  return narrow
}

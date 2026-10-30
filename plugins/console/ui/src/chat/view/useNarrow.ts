import { useEffect, useState } from 'react'

/** True below the kit's lg breakpoint (1024px), where the aside becomes an overlay and the spine narrows. */
export const useNarrow = () => useMedia('(max-width: 1023px)')
/** True at phone width (the golden's 640px break): the chat list is home, a thread pushes full width, sheets go full width. */
export const useMobile = () => useMedia('(max-width: 640px)')

function useMedia(QUERY: string): boolean {
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

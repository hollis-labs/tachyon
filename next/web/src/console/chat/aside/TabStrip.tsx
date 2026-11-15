import { useCallback, useEffect, useRef, useState, type ReactNode } from 'react'
import { ChevronLeft, ChevronRight } from 'lucide-react'
import { cn } from '@hollis-labs/design-components'

const ARROW = 'grid h-6 w-5 shrink-0 place-items-center rounded-control text-fg-secondary hover:text-fg focus-visible:outline focus-visible:outline-1 focus-visible:outline-ring disabled:invisible'

/**
 * The aside's tab strip: always one line, scrolls horizontally with no visible scrollbar. A chevron button sits at each
 * end and shows only when there are more tabs in that direction; the active tab is scrolled into view.
 * Tablist semantics stay on the scroller; the arrows are plain labelled buttons outside it.
 */
export function TabStrip({ label, activeKey, children }: { label: string; activeKey: string; children: ReactNode }) {
  const scroller = useRef<HTMLDivElement | null>(null)
  const [more, setMore] = useState({ left: false, right: false })

  const measure = useCallback(() => {
    const el = scroller.current
    if (!el) return
    const left = el.scrollLeft > 1
    const right = el.scrollLeft + el.clientWidth < el.scrollWidth - 1
    setMore((m) => (m.left === left && m.right === right ? m : { left, right }))
  }, [])

  useEffect(() => {
    measure()
    const el = scroller.current
    if (!el) return
    const ro = typeof ResizeObserver === 'function' ? new ResizeObserver(measure) : null
    ro?.observe(el)
    for (const c of Array.from(el.children)) ro?.observe(c)
    window.addEventListener('resize', measure)
    return () => { ro?.disconnect(); window.removeEventListener('resize', measure) }
  })

  // Keep the active tab visible when it changes (agent-opened or picked).
  useEffect(() => {
    const el = scroller.current?.querySelector('[aria-selected="true"]')
    if (el && typeof el.scrollIntoView === 'function') el.scrollIntoView({ block: 'nearest', inline: 'nearest' })
  }, [activeKey])

  const by = (dir: -1 | 1) => {
    const el = scroller.current
    if (!el) return
    const left = el.scrollLeft + dir * Math.max(80, Math.round(el.clientWidth * 0.7))
    if (typeof el.scrollTo === 'function') el.scrollTo({ left, behavior: 'smooth' })
    else el.scrollLeft = left
  }

  return (
    <div className="flex shrink-0 items-center gap-0.5 border-b border-border-subtle px-1 py-1.5">
      <button type="button" aria-label="Scroll tabs left" data-testid="tabs-left" disabled={!more.left} tabIndex={more.left ? 0 : -1} onClick={() => by(-1)} className={ARROW}><ChevronLeft aria-hidden className="size-3.5" /></button>
      <div ref={scroller} role="tablist" aria-label={label} onScroll={measure} className={cn('flex min-w-0 flex-1 flex-nowrap gap-0.5 overflow-x-auto overscroll-x-contain [-ms-overflow-style:none] [scrollbar-width:none] [&::-webkit-scrollbar]:hidden')}>
        {children}
      </div>
      <button type="button" aria-label="Scroll tabs right" data-testid="tabs-right" disabled={!more.right} tabIndex={more.right ? 0 : -1} onClick={() => by(1)} className={ARROW}><ChevronRight aria-hidden className="size-3.5" /></button>
    </div>
  )
}

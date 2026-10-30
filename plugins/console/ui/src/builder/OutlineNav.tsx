import { useEffect, useState } from 'react'
import { SECTIONS, type SectionId } from './vocab'

/** Anchors for a scrolling form; the section nearest the top is current. */
export function OutlineNav({ root }: { root: React.RefObject<HTMLElement | null> }) {
  const [current, setCurrent] = useState<SectionId>('identity')
  useEffect(() => {
    const el = root.current
    if (!el || typeof IntersectionObserver === 'undefined') return
    const seen = new Map<string, number>()
    const io = new IntersectionObserver(
      (entries) => {
        for (const e of entries) seen.set(e.target.id, e.isIntersecting ? e.boundingClientRect.top : Number.POSITIVE_INFINITY)
        const top = [...seen.entries()].filter(([, t]) => Number.isFinite(t)).sort((a, b) => a[1] - b[1])[0]
        if (top) setCurrent(top[0].replace('section-', '') as SectionId)
      },
      { root: el, rootMargin: '0px 0px -70% 0px' },
    )
    for (const s of SECTIONS) {
      const target = el.querySelector(`#section-${s.id}`)
      if (target) io.observe(target)
    }
    return () => io.disconnect()
  }, [root])

  const go = (id: SectionId) => {
    const reduce = window.matchMedia?.('(prefers-reduced-motion: reduce)').matches
    root.current?.querySelector(`#section-${id}`)?.scrollIntoView({ behavior: reduce ? 'auto' : 'smooth', block: 'start' })
    setCurrent(id)
  }
  return (
    <nav aria-label="Agent sections" className="hidden w-40 shrink-0 flex-col gap-0.5 border-r border-border p-2 lg:flex">
      {SECTIONS.map((s) => (
        <button
          key={s.id}
          type="button"
          aria-current={current === s.id ? 'true' : undefined}
          onClick={() => go(s.id)}
          className={`px-2 py-1 text-left dash-control ${current === s.id ? 'bg-surface font-medium text-fg' : 'text-fg-muted hover:text-fg'}`}
        >
          {s.label}
        </button>
      ))}
    </nav>
  )
}

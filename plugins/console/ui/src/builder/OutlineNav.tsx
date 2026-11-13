import { useEffect, useState } from 'react'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@hollis-labs/design-components'
import { useKeys } from './keys'
import { SECTIONS, type SectionId } from './vocab'

/** Anchors for a scrolling form; the section nearest the top is current. The rail shows from 1024; below it the same sections are a select. */
export function OutlineNav({ root, variant = 'rail' }: { root: React.RefObject<HTMLElement | null>; variant?: 'rail' | 'select' }) {
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
  const stepSection = (by: 1 | -1) => {
    const i = SECTIONS.findIndex((s) => s.id === current)
    const next = SECTIONS[Math.min(SECTIONS.length - 1, Math.max(0, i + by))]
    if (next && next.id !== current) go(next.id)
  }
  // One instance owns the keys so [ and ] do not fire twice.
  useKeys(variant === 'rail' ? { '[': () => stepSection(-1), ']': () => stepSection(1) } : {})
  if (variant === 'select')
    return (
      <div className="shrink-0 border-b border-border-subtle px-4 py-2 lg:hidden">
        <Select value={current} onValueChange={(v) => v != null && go(String(v) as SectionId)}>
          <SelectTrigger className="h-7 w-full text-xs" aria-label="Go to section">
            <SelectValue>{(v: unknown) => SECTIONS.find((s) => s.id === v)?.label ?? 'Section'}</SelectValue>
          </SelectTrigger>
          <SelectContent>
            {SECTIONS.map((s) => (
              <SelectItem key={s.id} value={s.id}>
                {s.label}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </div>
    )
  return (
    <nav aria-label="Agent sections" className="hidden w-[150px] shrink-0 flex-col gap-0.5 border-r border-border-subtle py-3.5 pl-4 pr-2 lg:flex">
      {SECTIONS.map((s) => (
        <button
          key={s.id}
          type="button"
          aria-current={current === s.id ? 'true' : undefined}
          onClick={() => go(s.id)}
          className={`rounded-control px-2 py-[5px] text-left text-xs ${current === s.id ? 'bg-selection font-semibold text-fg' : 'text-fg-muted hover:text-fg'}`}
        >
          {s.label}
        </button>
      ))}
    </nav>
  )
}

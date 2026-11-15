import { useEffect, useRef } from 'react'
import type { PanelId } from '../aside/context'

const FIELD = 'input, textarea, select, [contenteditable=""], [contenteditable="true"], [role=dialog], [role=menu]'
const CHORD: Record<string, PanelId> = { p: 'plan', a: 'approvals', w: 'work', l: 'activity', f: 'files' }

/**
 * Bare-key chat shortcuts, active only when focus is not in a field or dialog:
 * G then P/A/W/L/F opens a panel, G then N goes to the first card waiting on you, B bookmarks the focused (or last)
 * message, J/K move between messages, A adds a plan step and C comments while their panel is open.
 */
export function useChatKeys({ pick, panel, open, toggleBookmark }: { pick: (p: PanelId) => void; panel: PanelId; open: boolean; toggleBookmark: (messageRef: string) => void }) {
  const chord = useRef<number>(0)
  const latest = useRef({ pick, panel, open, toggleBookmark })
  latest.current = { pick, panel, open, toggleBookmark }
  useEffect(() => {
    const articles = () => Array.from(document.querySelectorAll<HTMLElement>('main[aria-label="Conversation"] article[data-from]'))
    const on = (e: KeyboardEvent) => {
      if (e.metaKey || e.ctrlKey || e.altKey || e.defaultPrevented) return
      const t = e.target instanceof Element ? e.target : null
      if (t?.closest(FIELD)) return
      const k = e.key.toLowerCase()
      const cur = latest.current
      if (chord.current && Date.now() - chord.current < 1000) {
        chord.current = 0
        if (k === 'n') { e.preventDefault(); document.querySelector<HTMLElement>('main[aria-label="Conversation"] section[role=group][tabindex="0"]')?.focus(); return }
        const p = CHORD[k]
        if (p) { e.preventDefault(); cur.pick(p) }
        return
      }
      if (k === 'g') { chord.current = Date.now(); return }
      if (k === 'b') {
        const a = articles()
        const focused = t?.closest('article[data-from]') as HTMLElement | null
        const target = focused ?? [...a].reverse().find((x) => x.dataset.from !== 'operator')
        if (target?.id) { e.preventDefault(); cur.toggleBookmark(target.id) }
      } else if (k === 'j' || k === 'k') {
        const a = articles()
        if (!a.length) return
        const i = a.findIndex((x) => x.contains(document.activeElement))
        const next = k === 'j' ? Math.min(a.length - 1, i + 1) : Math.max(0, i < 0 ? a.length - 1 : i - 1)
        e.preventDefault()
        a[next].focus()
        a[next].scrollIntoView?.({ block: 'nearest' })
      } else if (k === 'a' && cur.open && cur.panel === 'plan') {
        const b = document.querySelector<HTMLElement>('[data-add-step]')
        if (b) { e.preventDefault(); b.click() }
      } else if (k === 'c' && cur.open && cur.panel === 'activity') {
        const i = document.querySelector<HTMLElement>('input[aria-label=Comment]')
        if (i) { e.preventDefault(); i.focus() }
      }
    }
    window.addEventListener('keydown', on)
    return () => window.removeEventListener('keydown', on)
  }, [])
}

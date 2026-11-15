import { useSyncExternalStore } from 'react'

/** Longest chip label, in characters, before it is cut with an ellipsis. The hovercard carries the full title. */
export const DEFAULT_REF_LABEL_MAX = 48
export const REF_LABEL_MAX_RANGE = { min: 16, max: 200 } as const
export const REF_LABEL_MAX_KEY = 'console.refs.labelMax.v1'

const listeners = new Set<() => void>()

const clamp = (n: number) => Math.min(REF_LABEL_MAX_RANGE.max, Math.max(REF_LABEL_MAX_RANGE.min, Math.round(n)))

export function getRefLabelMax(): number {
  try {
    const raw = typeof window === 'undefined' ? null : window.localStorage.getItem(REF_LABEL_MAX_KEY)
    const n = raw === null ? NaN : Number(raw)
    return Number.isFinite(n) ? clamp(n) : DEFAULT_REF_LABEL_MAX
  } catch {
    return DEFAULT_REF_LABEL_MAX
  }
}

/** Per-device setting; `null` restores the default. Out-of-range values are clamped. */
export function setRefLabelMax(n: number | null): void {
  try {
    if (n === null) window.localStorage.removeItem(REF_LABEL_MAX_KEY)
    else window.localStorage.setItem(REF_LABEL_MAX_KEY, String(clamp(n)))
  } catch {
    /* private mode / quota: keep the default */
  }
  listeners.forEach((l) => l())
}

export function useRefLabelMax(): number {
  return useSyncExternalStore(
    (l) => {
      listeners.add(l)
      return () => listeners.delete(l)
    },
    getRefLabelMax,
    () => DEFAULT_REF_LABEL_MAX,
  )
}

/** Cut to `max` characters, ending in an ellipsis. Whitespace runs collapse so a multi-line title stays one line. */
export function truncateLabel(text: string, max: number): string {
  const t = text.replace(/\s+/g, ' ').trim()
  return t.length <= max ? t : `${t.slice(0, Math.max(1, max - 1)).trimEnd()}…`
}

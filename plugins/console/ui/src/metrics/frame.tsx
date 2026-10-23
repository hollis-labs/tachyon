import type { ReactNode } from 'react'

export const LABEL = 'font-mono text-micro uppercase tracking-label text-fg-muted'

/** Shared loading / error / empty placeholders so every widget degrades identically. */
export function Placeholder({ kind, label, message, height = 48 }: { kind: 'loading' | 'error' | 'empty'; label: string; message?: string; height?: number }) {
  if (kind === 'loading') {
    return (
      <div role="status" aria-busy="true" aria-label={`${label} loading`} className="animate-pulse bg-surface" style={{ height }} />
    )
  }
  return (
    <div
      role={kind === 'error' ? 'alert' : 'status'}
      className={`flex items-center justify-center border border-dashed border-border-subtle px-3 font-mono text-xs ${kind === 'error' ? 'text-danger' : 'text-fg-muted'}`}
      style={{ height }}
    >
      {message ?? (kind === 'error' ? 'Unavailable' : 'No data')}
    </div>
  )
}

export function Legend({ children }: { children: ReactNode }) {
  return <ul className="flex flex-wrap gap-x-4 gap-y-1 font-mono text-label text-fg-secondary">{children}</ul>
}

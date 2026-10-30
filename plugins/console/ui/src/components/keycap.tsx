import type { ReactNode } from 'react'

/** A key hint inside a control, e.g. the D on Deny. */
export function Keycap({ children }: { children: ReactNode }) {
  return <kbd aria-hidden className="inline-flex min-w-4 items-center justify-center rounded border border-border px-1 font-mono dash-caption text-fg-muted">{children}</kbd>
}

import type { ReactNode } from 'react'

const TONE = {
  neutral: 'border-border text-fg-muted',
  faint: 'border-border text-fg-faint',
  danger: 'border-danger/60 text-danger',
} as const

/** Square outlined chip. Danger is the only colour. */
export function Chip({ tone = 'neutral', children, className = '' }: { tone?: keyof typeof TONE; children: ReactNode; className?: string }) {
  return <span className={`inline-flex shrink-0 items-center gap-1 whitespace-nowrap rounded border px-1.5 font-mono dash-caption ${TONE[tone]} ${className}`}>{children}</span>
}

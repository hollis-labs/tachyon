import type { ReactNode } from 'react'
import { Link } from 'react-router-dom'
import { Chip } from '../components/chip'

/** Tile initials: first letters of the first two words, one letter for a single word. */
export function tileInitials(name: string): string {
  const words = name.trim().split(/[\s._:-]+/).filter(Boolean)
  return words
    .slice(0, 2)
    .map((w) => w[0])
    .join('')
    .toUpperCase()
}

/** Square tile avatar for agents and projects. */
export function Tile({ name, large = false }: { name: string; large?: boolean }) {
  return (
    <span
      aria-hidden
      className={`inline-flex shrink-0 items-center justify-center rounded-control bg-surface font-mono font-semibold text-fg-secondary ${large ? 'size-11 text-xs' : 'size-8 text-label'}`}
    >
      {tileInitials(name)}
    </span>
  )
}

/** Section heading: mono caps. */
export function Cap({ children, id }: { children: ReactNode; id?: string }) {
  return (
    <h2 id={id} className="font-mono text-caption uppercase tracking-label text-fg-muted">
      {children}
    </h2>
  )
}

/** One label and value, 130px label column. `hint` marks the value as editable on hover. */
export function Field({ label, children, className = '', stacked = false }: { label: string; children: ReactNode; className?: string; stacked?: boolean }) {
  return (
    <div className={`grid gap-0.5 py-[3px] ${stacked ? '' : 'sm:grid-cols-[130px_1fr] sm:gap-3'} ${className}`}>
      <dt className={`font-mono text-caption uppercase tracking-label text-fg-muted ${stacked ? '' : 'sm:pt-[7px]'}`}>{label}</dt>
      <dd className="-mx-2 -my-1 flex min-h-[30px] min-w-0 flex-wrap items-center gap-1.5 rounded-control px-2 py-1 text-control text-fg [overflow-wrap:anywhere] hover:bg-selection">{children}</dd>
    </div>
  )
}

/** Outlined chip that links to the thing it names. */
export function Cite({ to, children }: { to: string; children: ReactNode }) {
  return (
    <Link to={to} className="inline-flex h-[18px] items-center rounded border border-border px-1.5 font-mono text-caption text-fg-secondary hover:bg-selection hover:text-fg">
      {children} ↗
    </Link>
  )
}

/** A card in the right rail: caption, then label and value rows. */
export function RailCard({ title, aside, children }: { title: ReactNode; aside?: ReactNode; children: ReactNode }) {
  return (
    <section className="flex flex-col gap-2 rounded-panel border border-border p-3">
      <div className="flex items-center justify-between gap-2">
        <h2 className="font-mono text-caption uppercase tracking-label text-fg-muted">{title}</h2>
        {aside}
      </div>
      {children}
    </section>
  )
}

export function RailRow({ label, children, tone }: { label?: string; children: ReactNode; tone?: 'danger' }) {
  const color = tone === 'danger' ? 'text-danger' : 'text-fg-secondary'
  if (label === undefined) return <p className={`text-xs ${color}`}>{children}</p>
  return (
    <div className={`flex justify-between gap-2 text-xs ${color}`}>
      <span>{label}</span>
      <b className="text-right font-medium text-fg">{children}</b>
    </div>
  )
}

export const Muted = ({ children }: { children: ReactNode }) => <span className="text-xs text-fg-muted">{children}</span>

/** Used when a value is not known: say so, never zero. */
export const NotReported = () => <Chip tone="faint">not reported</Chip>

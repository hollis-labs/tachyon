import type { HTMLAttributes, ReactNode } from 'react'
import { cn } from '@hollis-labs/design-components'

/** Overline text: mono, caption step, uppercase, label tracking. */
export const CAPTION = 'font-mono text-caption uppercase tracking-label text-fg-muted'

/** A key cap, e.g. the "/" beside the search field. Generic; chat reuses it. */
export function Kbd({ className, ...props }: HTMLAttributes<HTMLElement>) {
  return <kbd className={cn('rounded-sm border border-border px-1.5 py-0.5 font-mono text-caption leading-none text-fg-muted', className)} {...props} />
}

export type ChipTone = 'neutral' | 'strong' | 'warning' | 'danger'

const CHIP_TONE: Record<ChipTone, string> = {
  neutral: 'border-border text-fg-secondary',
  strong: 'border-fg text-fg',
  warning: 'border-warning/30 text-warning',
  danger: 'border-danger/30 text-danger',
}

/** A small outlined chip. `square` is the mono id/kind chip; the default is the round status chip. */
export function Chip({ square, tone = 'neutral', selected, className, ...props }: HTMLAttributes<HTMLElement> & { square?: boolean; tone?: ChipTone; selected?: boolean }) {
  return (
    <span
      className={cn(
        'inline-flex h-5 items-center gap-1 whitespace-nowrap border px-1.5 text-label',
        square ? 'rounded-sm font-mono text-caption' : 'rounded-full px-2',
        CHIP_TONE[tone],
        selected && 'bg-selection text-fg',
        className,
      )}
      {...props}
    />
  )
}

export type DotState = 'done' | 'doing' | 'review' | 'blocked' | 'todo'

const DOT: Record<DotState, string> = {
  done: 'bg-fg/88',
  doing: 'bg-fg/62',
  review: 'bg-fg/40',
  blocked: 'bg-danger',
  todo: 'bg-fg-faint',
}

/** A status dot on the same monochrome ramp as the progress bar; blocked is the only colour. */
export function StateDot({ state, className }: { state: DotState; className?: string }) {
  return <i aria-hidden className={cn('inline-block size-1.5 shrink-0 rounded-full', DOT[state], className)} />
}

/** A caption with an optional trailing count. */
export function GroupHeading({ children, count, id, className }: { children: ReactNode; count?: ReactNode; id?: string; className?: string }) {
  return (
    <h3 id={id} className={cn('flex items-center justify-between px-2 pb-1 pt-3', CAPTION, className)}>
      <span>{children}</span>
      {count !== undefined && <span className="tabular-nums">{count}</span>}
    </h3>
  )
}

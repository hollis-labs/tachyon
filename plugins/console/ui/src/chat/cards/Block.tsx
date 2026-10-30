import type { KeyboardEvent, ReactNode } from 'react'
import { Button, cn } from '@hollis-labs/design-components'
import { CAPTION, Kbd } from '@planner/shared'

/** The flat answerable block: a thin left rule, a small icon and caption, then the rows. No filled or rounded box. */
export function Block({ icon, caption, title, aside, children, label, onKeyDown, className }: { icon: ReactNode; caption: string; title?: ReactNode; aside?: ReactNode; children?: ReactNode; label: string; onKeyDown?: (e: KeyboardEvent<HTMLElement>) => void; className?: string }) {
  return (
    <section role="group" aria-label={label} tabIndex={onKeyDown ? 0 : undefined} onKeyDown={onKeyDown} className={cn('mx-auto flex w-full max-w-[760px] flex-col gap-2 border-l border-border py-0.5 pl-3.5 focus-visible:outline focus-visible:outline-1 focus-visible:outline-ring', className)}>
      <div className="flex items-center gap-2">
        <span aria-hidden className="grid w-[18px] place-items-center text-fg-secondary">{icon}</span>
        <span className={CAPTION}>{caption}</span>
        {title && <span className="min-w-0 truncate text-xs text-fg-muted">{title}</span>}
        <span className="flex-1" />
        {aside}
      </div>
      {children}
    </section>
  )
}

/** A card action: primary (filled) or outline, with an optional key hint. */
export function Act({ children, kbd, primary, danger, className, ...props }: { kbd?: string; primary?: boolean; danger?: boolean } & React.ComponentProps<typeof Button>) {
  return (
    <Button type="button" size="sm" variant={primary ? 'default' : 'outline'} className={cn('h-7 gap-1.5 px-2.5 text-label', danger && 'text-danger', className)} {...props}>
      {children}
      {kbd && <Kbd className={primary ? 'border-primary-fg/30 text-primary-fg' : undefined}>{kbd}</Kbd>}
    </Button>
  )
}

export const LINK = 'text-label text-fg-secondary hover:text-fg focus-visible:outline focus-visible:outline-1 focus-visible:outline-ring'

import { useRef, type ReactNode } from 'react'
import { Button, cn } from '@hollis-labs/design-components'

export interface TriageRoute {
  id: string
  label: string
  /** One-key shortcut. */
  key: string
}

export interface TriageStackLabels {
  /** "{n} left" */
  count: (n: number) => string
  allTriaged: string
  routeHeading: string
  dismiss: string
  /** "Next: {title}" */
  next: (title: string) => string
}

/** One card at a time with keyed routing. The host owns the data and moves to the next card when a write settles. */
export function TriageStack({ card, remaining, nextTitle, routes, dismissKey = 'x', onRoute, onDismiss, busy, labels, className }: { card: ReactNode | null; remaining: number; nextTitle?: string; routes: readonly TriageRoute[]; dismissKey?: string; onRoute: (id: string) => void; onDismiss: () => void; busy?: boolean; labels: TriageStackLabels; className?: string }) {
  const root = useRef<HTMLDivElement>(null)
  if (card === null) return <p className={cn('py-6 dash-support text-fg-muted', className)}>{labels.allTriaged}</p>
  return (
    <div
      ref={root}
      tabIndex={0}
      className={cn('flex flex-col gap-3 outline-none', className)}
      onKeyDown={(e) => {
        if (busy || e.metaKey || e.ctrlKey || e.altKey) return
        const t = e.target as HTMLElement
        if (t.tagName === 'INPUT' || t.tagName === 'TEXTAREA') return
        const k = e.key.toLowerCase()
        if (k === dismissKey) (e.preventDefault(), onDismiss())
        else {
          const r = routes.find((x) => x.key.toLowerCase() === k)
          if (r) (e.preventDefault(), onRoute(r.id))
        }
      }}
    >
      <p className="dash-caption text-fg-faint">{labels.count(remaining)}</p>
      <div className="rounded-md border border-border p-3">{card}</div>
      <div role="group" aria-label={labels.routeHeading} className="flex flex-wrap items-center gap-2">
        <span className="dash-caption text-fg-muted">{labels.routeHeading}</span>
        {routes.map((r) => (
          <Button key={r.id} size="sm" variant="outline" disabled={busy} onClick={() => onRoute(r.id)}>
            {r.label} <kbd className="ml-1 font-mono dash-caption text-fg-faint">{r.key}</kbd>
          </Button>
        ))}
        <Button size="sm" variant="ghost" disabled={busy} onClick={onDismiss}>
          {labels.dismiss} <kbd className="ml-1 font-mono dash-caption text-fg-faint">{dismissKey}</kbd>
        </Button>
      </div>
      {nextTitle && <p className="dash-caption text-fg-faint">{labels.next(nextTitle)}</p>}
    </div>
  )
}

import { useRef, type ReactNode } from 'react'
import { cn } from '@hollis-labs/design-components'
import { CAPTION, Kbd } from './primitives'

export interface TriageRoute {
  id: string
  label: string
  /** One-key shortcut. */
  key: string
}

export interface TriageStackLabels {
  /** "INBOX · {n} to triage": the pane's heading. */
  heading: (n: number) => string
  /** "{i} of {n}" */
  position: (i: number, n: number) => string
  allTriaged: string
  routeHeading: string
  dismiss: string
}

/**
 * One card at a time over a stack, with keyed routing. The host owns the data and moves to the next card
 * when a write settles; `index` is the card's place among `remaining`, and the pager's J / K are the host's.
 */
export function TriageStack({ card, index, remaining, routes, dismissKey = 'x', onRoute, onDismiss, busy, labels, className }: { card: ReactNode | null; index: number; remaining: number; routes: readonly TriageRoute[]; dismissKey?: string; onRoute: (id: string) => void; onDismiss: () => void; busy?: boolean; labels: TriageStackLabels; className?: string }) {
  const root = useRef<HTMLDivElement>(null)
  if (card === null) return <p className={cn('py-6 text-control text-fg-muted', className)}>{labels.allTriaged}</p>
  const button = 'flex h-9 items-center justify-between rounded-panel border border-border px-3 text-control text-fg hover:bg-surface-hover disabled:opacity-50'
  return (
    <div
      ref={root}
      tabIndex={0}
      className={cn('flex flex-col gap-5 outline-none', className)}
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
      <div className="flex items-center justify-between gap-3">
        <h2 className={CAPTION}>{labels.heading(remaining)}</h2>
        <span className="flex items-center gap-1.5 text-control text-fg-muted">
          {labels.position(index + 1, remaining)}
          <Kbd>J</Kbd>
          <Kbd>K</Kbd>
        </span>
      </div>
      <div className="relative">
        <div className="rounded-panel border border-border bg-bg-elevated p-4">{card}</div>
        {remaining > 1 && <div aria-hidden className="mx-3 h-2 rounded-b-panel border border-t-0 border-border bg-bg-elevated" />}
      </div>
      <section aria-label={labels.routeHeading} className="flex flex-col gap-2">
        <h2 className={CAPTION}>{labels.routeHeading}</h2>
        <div role="group" aria-label={labels.routeHeading} className="grid grid-cols-3 gap-2 max-sm:grid-cols-1">
          {routes.map((r) => (
            <button key={r.id} type="button" disabled={busy} aria-label={r.label} onClick={() => onRoute(r.id)} className={button}>
              {r.label}
              <Kbd>{r.key}</Kbd>
            </button>
          ))}
          <button type="button" disabled={busy} aria-label={labels.dismiss} onClick={onDismiss} className={button}>
            {labels.dismiss}
            <Kbd>{dismissKey.toUpperCase()}</Kbd>
          </button>
        </div>
      </section>
    </div>
  )
}

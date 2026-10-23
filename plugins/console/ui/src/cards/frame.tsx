// Shared card chrome: every kind renders inside CardFrame so cards look alike
// on every surface. Divider-based, no shadows; controls come from design-components.
import { Button } from '@hollis-labs/design-components'
import type { ReactNode } from 'react'
import { CARD_SEVERITY_STYLE, relTime } from './frame-util'
import type { CardActionController, CardActionSpec, CardDensity, CardInstance } from './types'

const VARIANT = { primary: 'default', neutral: 'outline', danger: 'destructive' } as const satisfies Record<CardActionSpec['intent'], string>

export function CardActions({ card, controller, disabled }: { card: CardInstance; controller: CardActionController; disabled?: boolean }) {
  const settled = card.phase !== 'live'
  const last = Object.values(controller.status).find((s) => s.message && !s.pending)
  const anyPending = Object.values(controller.status).some((s) => s.pending)
  if (card.actions.length === 0 && settled) return null
  return (
    <div className="flex flex-wrap items-center gap-2 pt-2">
      {card.actions.map((a) => {
        const st = controller.status[a.id]
        return (
          <Button
            key={a.id}
            type="button"
            variant={VARIANT[a.intent]}
            size="xs"
            disabled={disabled || settled || anyPending}
            onClick={() => void controller.run(a)}
            className="font-mono uppercase tracking-label"
          >
            {st?.pending ? `${a.label}...` : a.label}
            {a.confirm ? <span aria-label="requires identity confirmation" className="ml-1 opacity-70">*</span> : null}
          </Button>
        )
      })}
      {!settled && (
        <>
          <Button type="button" variant="ghost" size="xs" disabled={anyPending} onClick={() => void controller.snooze(3_600_000)} className="font-mono uppercase tracking-label text-fg-muted">Snooze 1h</Button>
          <Button type="button" variant="ghost" size="xs" disabled={anyPending} onClick={() => void controller.dismiss()} className="font-mono uppercase tracking-label text-fg-muted">Dismiss</Button>
        </>
      )}
      {last?.message && (
        <span role={last.ok ? 'status' : 'alert'} className={`font-mono text-label ${last.ok ? 'text-success' : 'text-danger'}`}>{last.message}</span>
      )}
    </div>
  )
}

export function PhaseBadge({ card }: { card: CardInstance }) {
  if (card.phase === 'live') return null
  const label = card.phase === 'resolved' ? (card.resolution?.label ?? 'Resolved') : card.phase === 'expired' ? 'Expired' : 'Superseded'
  return <span className="border border-border px-1.5 py-px font-mono text-micro uppercase tracking-label text-fg-secondary">{label}</span>
}

export function CardFrame({
  card,
  density,
  kindLabel,
  children,
  footer,
  onExpand,
}: {
  card: CardInstance
  density: CardDensity
  kindLabel: string
  children?: ReactNode
  footer?: ReactNode
  onExpand?: () => void
}) {
  const sev = CARD_SEVERITY_STYLE[card.severity]
  const compact = density === 'compact'
  const dim = card.phase !== 'live'
  return (
    <article data-card-kind={card.kind} data-card-phase={card.phase} aria-label={`${kindLabel}: ${card.title}`} className={`relative flex w-full border-b border-border-subtle ${dim ? 'opacity-70' : ''}`}>
      <span aria-hidden className={`w-0.5 shrink-0 ${dim ? 'bg-border' : sev.rail}`} />
      <div className={`min-w-0 flex-1 ${compact ? 'px-3 py-2' : 'px-4 py-3'}`}>
        <header className="flex items-center gap-2">
          <span className={`font-mono text-micro uppercase tracking-label ${dim ? 'text-fg-muted' : sev.text}`}>{kindLabel}</span>
          <PhaseBadge card={card} />
          {card.count > 1 && <span className="font-mono text-micro text-fg-muted">x{card.count}</span>}
          <span className="ml-auto font-mono text-micro text-fg-muted">{relTime(card.ts)}</span>
        </header>
        {onExpand && compact ? (
          <button type="button" onClick={onExpand} className="mt-0.5 block w-full truncate text-left text-sm text-fg hover:underline focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring">{card.title}</button>
        ) : (
          <h3 className={`mt-0.5 text-fg ${compact ? 'truncate text-sm' : 'text-sm font-medium'}`}>{card.title}</h3>
        )}
        {card.summary && <p className={`text-xs text-fg-secondary ${compact ? 'truncate' : 'mt-1'}`}>{card.summary}</p>}
        {children}
        {footer}
      </div>
    </article>
  )
}

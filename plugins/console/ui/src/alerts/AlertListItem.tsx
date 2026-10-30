import { Button } from '@hollis-labs/design-components'
import { ago } from '@console/lib/agent-activity'
import { AlertIcon } from './AlertIcon'
import { AlertLinkButton } from './AlertLinkButton'
import { SEVERITY_STYLE } from './severity-style'
import type { ItemStatus } from '../seams/state-store'
import type { Alert } from './types'
import { LinkText } from '../autolink/LinkText'

export interface AlertListItemProps {
  alert: Alert
  status?: ItemStatus
  snoozeUntil?: number
  now: number
  /** Called when the primary link is followed. */
  onRead?: () => void
  onDismiss?: () => void
  onSnooze?: () => void
  onRestore?: () => void
  /** Opens the alert in the detail modal (A2). */
  onExpand?: () => void
}

const BTN = 'text-fg-muted hover:text-fg'

/** One alert row: icon, severity stripe, title, one-line detail, source/age, primary link, state actions. */
export function AlertListItem({ alert, status = 'unread', snoozeUntil, now, onRead, onDismiss, onSnooze, onRestore, onExpand }: AlertListItemProps) {
  const sev = SEVERITY_STYLE[alert.severity]
  const unread = status === 'unread'
  const handled = status === 'dismissed' || status === 'snoozed' || status === 'settled'
  return (
    <li
      data-testid="alert-item"
      data-status={status}
      className={`group relative flex gap-3 border-b border-border-subtle px-4 py-2.5 ${handled ? 'opacity-60' : ''}`}
    >
      <span className="pt-0.5">
        <AlertIcon icon={alert.icon} severity={alert.severity} />
      </span>
      <div className="min-w-0 flex-1">
        <div className="flex items-baseline gap-2">
          {onExpand ? (
            <button type="button" onClick={onExpand} aria-label={`Open details: ${alert.title}`} className={`min-w-0 flex-1 truncate text-left text-[13px] hover:underline focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring ${unread ? 'font-semibold text-fg' : 'text-fg-secondary'}`}>
              {alert.title}
            </button>
          ) : (
            <span className={`min-w-0 flex-1 truncate text-[13px] ${unread ? 'font-semibold text-fg' : 'text-fg-secondary'}`}>{alert.title}</span>
          )}
          {unread && <span className="h-1.5 w-1.5 shrink-0 rounded-full bg-fg" role="img" aria-label="unread" />}
        </div>
        {alert.detail && <p className="truncate text-xs text-fg-muted"><LinkText surface="alerts" text={alert.detail} /></p>}
        <p className="mt-0.5 flex flex-wrap items-center gap-x-2 text-xs text-fg-faint">
          {sev.named && <span className={sev.text}>{sev.label}</span>}
          <span>{alert.source}</span>
          <span>{ago(alert.ts, now)}</span>
          {status === 'snoozed' && snoozeUntil && <span>snoozed until {new Date(snoozeUntil).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })}</span>}
          {status === 'dismissed' && <span>dismissed</span>}
        </p>
        {alert.primary && (
          <div className="mt-1.5 flex flex-wrap items-center gap-1.5">
            <AlertLinkButton link={alert.primary} onFollow={onRead} />
          </div>
        )}
        {(onDismiss || onSnooze || onRestore) && (
          <div className="mt-1 flex items-center gap-1.5 md:absolute md:right-3 md:top-1.5 md:mt-0 md:group-hover:bg-bg-elevated md:group-focus-within:bg-bg-elevated">
            <span className="flex items-center gap-1.5 transition-opacity group-focus-within:opacity-100 group-hover:opacity-100 md:opacity-0">
            {onSnooze && (
              <Button type="button" variant="ghost" size="xs" className={BTN} onClick={onSnooze} aria-label={`Snooze for 1 hour: ${alert.title}`}>
                Snooze 1h
              </Button>
            )}
            {onDismiss && (
              <Button type="button" variant="ghost" size="xs" className={BTN} onClick={onDismiss} aria-label={`Dismiss: ${alert.title}`}>
                Dismiss
              </Button>
            )}
            {onRestore && (
              <Button type="button" variant="ghost" size="xs" className={BTN} onClick={onRestore} aria-label={`Restore: ${alert.title}`}>
                Restore
              </Button>
            )}
            </span>
          </div>
        )}
      </div>
    </li>
  )
}

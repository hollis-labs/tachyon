import { Button } from '@hollis-labs/design-components'
import { ago } from '@console/lib/agent-activity'
import { AlertIcon } from './AlertIcon'
import { AlertLinkButton } from './AlertLinkButton'
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

const BTN = 'text-fg-muted hover:text-fg max-md:min-h-10 max-md:min-w-10'
const age = (ts: number, now: number) => ago(ts, now).replace(/ ago$/, '')

/** One alert row: rail icon, title (with a x4 for repeats), one-line sub, age. Actions appear on hover or focus. */
export function AlertListItem({ alert, status = 'unread', snoozeUntil, now, onRead, onDismiss, onSnooze, onRestore, onExpand }: AlertListItemProps) {
  const unread = status === 'unread'
  const handled = status === 'dismissed' || status === 'snoozed' || status === 'settled'
  const title = alert.count && alert.count > 1 ? `${alert.title} ×${alert.count}` : alert.title
  const sub = status === 'snoozed' && snoozeUntil
    ? `Snoozed until ${new Date(snoozeUntil).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })}`
    : status === 'dismissed' ? 'Dismissed' : null
  return (
    <li
      data-testid="alert-item"
      data-alert-id={alert.id}
      data-status={status}
      className={`group relative flex gap-3 px-4 py-2.5 hover:bg-surface/30 ${handled ? 'opacity-60' : ''}`}
    >
      <span className="pt-0.5">
        <AlertIcon icon={alert.icon} severity={alert.severity} />
      </span>
      <div className="min-w-0 flex-1">
        <div className="flex items-baseline gap-2">
          {onExpand ? (
            <button type="button" onClick={onExpand} aria-label={`Open details: ${alert.title}`} className={`min-w-0 truncate text-left dash-control hover:underline focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring ${unread ? 'font-semibold text-fg' : 'text-fg-secondary'}`}>
              {title}
            </button>
          ) : (
            <span className={`min-w-0 truncate dash-control ${unread ? 'font-semibold text-fg' : 'text-fg-secondary'}`}>{title}</span>
          )}
          {unread && <span className="size-1.5 shrink-0 self-center rounded-full bg-fg" role="img" aria-label="unread" />}
          <span className="ml-auto shrink-0 font-mono dash-caption text-fg-faint">{age(alert.ts, now)}</span>
        </div>
        {(alert.detail || sub) && <p className="line-clamp-1 dash-support text-fg-muted">{sub ?? <LinkText surface="alerts" text={alert.detail ?? ''} />}</p>}
        {(alert.primary || onDismiss || onSnooze || onRestore) && (
          <div className="mt-1 flex flex-wrap items-center gap-1.5 transition-opacity group-focus-within:opacity-100 group-hover:opacity-100 md:h-0 md:overflow-hidden md:opacity-0 md:group-focus-within:h-auto md:group-hover:h-auto">
            {alert.primary && <AlertLinkButton link={alert.primary} onFollow={onRead} />}
            {onSnooze && (
              <Button type="button" variant="ghost" size="xs" className={BTN} onClick={onSnooze} aria-label={`Snooze for 1 hour: ${alert.title}`}>
                Snooze
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
          </div>
        )}
      </div>
    </li>
  )
}

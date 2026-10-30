import type { AlertSeverity } from './types'

/** Unread marker: a plain neutral dot up to 9, "9+" above. Nothing at zero. The count is also in the trigger's accessible name. */
export function AlertBadge({ count, className = '' }: { count: number; severity?: AlertSeverity; className?: string }) {
  if (count <= 0) return null
  return (
    <span data-testid="alert-badge" aria-hidden="true" className={`inline-flex ${className}`}>
      {count > 9 ? (
        <span className="rounded-full bg-fg px-1 text-[10px] font-semibold leading-4 text-bg">9+</span>
      ) : (
        <span className="h-2 w-2 rounded-full bg-fg" />
      )}
    </span>
  )
}

import { Pill } from '@hollis-labs/design-components'
import { SEVERITY_STYLE } from './severity-style'
import type { AlertSeverity } from './types'

/** Count badge. Renders nothing at zero. The count is also in the trigger's accessible name. */
export function AlertBadge({ count, severity = 'info', className = '' }: { count: number; severity?: AlertSeverity; className?: string }) {
  if (count <= 0) return null
  const sev = SEVERITY_STYLE[severity]
  return (
    <span data-testid="alert-badge" aria-hidden="true" className={`inline-flex ${className}`}>
      <Pill tone={sev.tone} className={`min-w-4 justify-center px-1 font-mono text-micro font-semibold leading-none ${sev.solid ? 'border-danger bg-danger text-danger-fg' : ''}`}>
        {count > 99 ? '99+' : count}
      </Pill>
    </span>
  )
}

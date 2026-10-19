import { Tooltip, TooltipContent, TooltipTrigger } from '@hollis-labs/design-components'
import { StatusBadge as KitStatusBadge, type StatusKey } from '@hollis-labs/kit-dashboard'
const CONTAINER_TONES: Record<string, StatusKey> = { active: 'done', inactive: 'archived', completed: 'doing' }

interface StatusBadgeProps {
  status: string
  className?: string
  /**
   * Optional tooltip text shown on hover/focus. Used to surface the
   * blocked_reason on a status pill without making the user click through.
   */
  tooltip?: string
}

/**
 * Torque status pill — a thin domain wrapper over the kit's `StatusBadge`.
 * Adds two Torque-specific concerns the generic kit `StatusBadge` doesn't
 * carry: container statuses (`active`/`inactive`/`completed`) and an optional
 * hover tooltip for surfacing a blocked reason.
 */
export function StatusBadge({ status, className = '', tooltip }: StatusBadgeProps) {
  const normalized = (status || '').toLowerCase()
  const badge = <KitStatusBadge status={status} tone={CONTAINER_TONES[normalized]} className={className} />

  if (!tooltip) return badge

  return (
    <Tooltip>
      <TooltipTrigger
        render={(props) => (
          <span {...props} className="inline-flex" tabIndex={0}>
            {badge}
          </span>
        )}
      />
      <TooltipContent className="max-w-sm whitespace-pre-wrap text-left">
        {tooltip}
      </TooltipContent>
    </Tooltip>
  )
}

import type { ReactNode } from 'react'
import { AdminBadge } from '@hollis-labs/kit-admin'
import type { Risk } from './types'
import type { PlanStatus } from './plan'

import { RISK_STYLE } from './ui-util'

export { AdminHeading as Heading, AdminSection as Block, AdminActionButton as Btn } from '@hollis-labs/kit-admin'

export function Chip({ tone, ...props }: { children: ReactNode; tone?: string }) {
  return <AdminBadge {...props} className={tone} />
}

export function RiskBadge({ risk }: { risk: Risk }) {
  return <AdminBadge className={`text-caption uppercase tracking-widest ${RISK_STYLE[risk]}`}>{risk} risk</AdminBadge>
}

const STATUS_TONE: Record<PlanStatus, string> = {
  demonstrated: 'border-success/50 text-success',
  proposed: 'border-info/50 text-info',
  invalid: 'border-danger/60 text-danger',
  approved: 'border-success/50 text-success',
  applied: 'border-success/50 text-success',
  failed: 'border-danger/60 text-danger',
  conflict: 'border-warning/50 text-warning',
  rolled_back: 'border-border text-fg-muted',
  expired: 'border-warning/50 text-warning',
}

export function StatusBadge({ status }: { status: PlanStatus }) {
  return <AdminBadge className={`text-caption uppercase tracking-widest ${STATUS_TONE[status]}`}>{status.replace('_', ' ')}</AdminBadge>
}

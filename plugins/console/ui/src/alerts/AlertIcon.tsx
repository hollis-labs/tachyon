import { AlertTriangle, Bot, CheckSquare, GitPullRequest, Info, MessageSquare, OctagonAlert, ShieldQuestion, type LucideIcon } from 'lucide-react'
import { SEVERITY_STYLE } from './severity-style'
import type { AlertIconKey, AlertSeverity } from './types'

const ICONS: Record<AlertIconKey, LucideIcon> = {
  info: Info,
  task: CheckSquare,
  agent: Bot,
  message: MessageSquare,
  approval: ShieldQuestion,
  pr: GitPullRequest,
  warning: AlertTriangle,
  error: OctagonAlert,
}

export function AlertIcon({ icon, severity }: { icon: AlertIconKey; severity: AlertSeverity }) {
  const I = ICONS[icon]
  const danger = SEVERITY_STYLE[severity].tone === 'danger'
  return (
    <span aria-hidden="true" className={`flex h-7 w-7 shrink-0 items-center justify-center rounded-full border ${danger ? 'border-danger/60 text-danger' : 'border-border text-fg-muted'}`}>
      <I className="h-3.5 w-3.5" />
    </span>
  )
}

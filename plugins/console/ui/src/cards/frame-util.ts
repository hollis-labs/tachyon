import type { ConsoleSeverity } from '../events/types'

export const CARD_SEVERITY_STYLE: Record<ConsoleSeverity, { rail: string; text: string; label: string }> = {
  debug: { rail: 'bg-border', text: 'text-fg-muted', label: 'DEBUG' },
  info: { rail: 'bg-info', text: 'text-info', label: 'INFO' },
  notice: { rail: 'bg-success', text: 'text-success', label: 'NOTICE' },
  warning: { rail: 'bg-warning', text: 'text-warning', label: 'WARNING' },
  error: { rail: 'bg-danger', text: 'text-danger', label: 'ERROR' },
  critical: { rail: 'bg-danger', text: 'text-danger', label: 'CRITICAL' },
}

export function relTime(ts: number, now = Date.now()): string {
  const s = Math.max(0, Math.round((now - ts) / 1000))
  if (s < 45) return 'now'
  if (s < 3600) return `${Math.round(s / 60)}m`
  if (s < 86_400) return `${Math.round(s / 3600)}h`
  return `${Math.round(s / 86_400)}d`
}


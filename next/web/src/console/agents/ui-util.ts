import type { Risk } from './types'

export const RISK_STYLE: Record<Risk, string> = {
  low: 'border-border text-fg-muted',
  medium: 'border-warning/50 text-warning',
  high: 'border-danger/60 text-danger',
}

export const clock = (ms: number) => new Date(ms).toISOString().slice(0, 19).replace('T', ' ') + 'Z'

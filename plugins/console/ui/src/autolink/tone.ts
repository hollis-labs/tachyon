import type { StatusTone } from '../refs'

/** Square, outlined status colours shared by the card and drawer (Operations dashboard style), on design tokens. */
export const TONE: Record<StatusTone, string> = {
  neutral: 'border-border text-fg-secondary',
  info: 'border-info text-info',
  success: 'border-success text-success',
  warning: 'border-warning text-warning',
  danger: 'border-destructive text-destructive',
}

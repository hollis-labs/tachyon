import type { Tone } from '@hollis-labs/design-components'
import type { AlertSeverity } from './types'

export interface SeverityStyle {
  label: string
  /** Left stripe + dot (contract tone fill). */
  bar: string
  /** Text colour for the severity label and icon. */
  text: string
  /** Contract tone for pills and count badges. */
  tone: Tone
  /** Critical is the one solid badge; everything else is a muted tint. Severity is never colour alone: label text and icon always accompany it. */
  solid: boolean
}

export const SEVERITY_STYLE: Record<AlertSeverity, SeverityStyle> = {
  debug: { label: 'DEBUG', bar: 'bg-fg-faint', text: 'text-fg-faint', tone: 'neutral', solid: false },
  info: { label: 'INFO', bar: 'bg-info', text: 'text-info', tone: 'info', solid: false },
  notice: { label: 'NOTICE', bar: 'bg-success', text: 'text-success', tone: 'success', solid: false },
  warning: { label: 'WARNING', bar: 'bg-warning', text: 'text-warning', tone: 'warning', solid: false },
  error: { label: 'ERROR', bar: 'bg-danger', text: 'text-danger', tone: 'danger', solid: false },
  critical: { label: 'CRITICAL', bar: 'bg-danger', text: 'text-danger', tone: 'danger', solid: true },
}

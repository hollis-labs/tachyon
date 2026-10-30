import type { Tone } from '@hollis-labs/design-components'
import type { AlertSeverity } from './types'

export interface SeverityStyle {
  /** Sentence-case name; shown as text next to the icon so severity is never colour alone. */
  label: string
  /** Stripe/dot fill. */
  bar: string
  /** Text colour for the severity label and icon. */
  text: string
  /** Contract tone for pills and count badges. */
  tone: Tone
  /** Critical is the one solid badge; everything else is a muted tint. */
  solid: boolean
  /** True when the severity should be named in the row (warning and above). */
  named: boolean
}

// Monochrome: danger is the only colour (error, critical). Everything else is quiet.
export const SEVERITY_STYLE: Record<AlertSeverity, SeverityStyle> = {
  debug: { label: 'Debug', bar: 'bg-fg-faint', text: 'text-fg-faint', tone: 'neutral', solid: false, named: false },
  info: { label: 'Info', bar: 'bg-fg-faint', text: 'text-fg-muted', tone: 'neutral', solid: false, named: false },
  notice: { label: 'Notice', bar: 'bg-fg-faint', text: 'text-fg-muted', tone: 'neutral', solid: false, named: false },
  warning: { label: 'Warning', bar: 'bg-fg-muted', text: 'text-fg', tone: 'neutral', solid: false, named: true },
  error: { label: 'Error', bar: 'bg-danger', text: 'text-danger', tone: 'danger', solid: false, named: true },
  critical: { label: 'Critical', bar: 'bg-danger', text: 'text-danger', tone: 'danger', solid: true, named: true },
}

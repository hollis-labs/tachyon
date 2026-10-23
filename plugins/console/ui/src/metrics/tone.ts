import type { MetricStatus, Tone } from './types'

/** Theme mapping in one place: swap these classes to retheme the whole kit. */
export const TONE: Record<Tone, { text: string; bg: string; stroke: string }> = {
  neutral: { text: 'text-fg-secondary', bg: 'bg-fg-muted', stroke: 'stroke-fg-muted' },
  good: { text: 'text-success', bg: 'bg-success', stroke: 'stroke-success' },
  warn: { text: 'text-warning', bg: 'bg-warning', stroke: 'stroke-warning' },
  bad: { text: 'text-danger', bg: 'bg-danger', stroke: 'stroke-danger' },
  info: { text: 'text-info', bg: 'bg-info', stroke: 'stroke-info' },
}

export const STATUS_TONE: Record<MetricStatus, Tone> = {
  done: 'good',
  in_progress: 'info',
  review: 'warn',
  blocked: 'bad',
  todo: 'neutral',
}

export const STATUS_LABEL: Record<MetricStatus, string> = {
  done: 'Done',
  in_progress: 'In progress',
  review: 'Review',
  blocked: 'Blocked',
  todo: 'To do',
}

export function toneForStatus(status: string): Tone {
  return STATUS_TONE[status as MetricStatus] ?? 'neutral'
}

export function labelForStatus(status: string): string {
  return STATUS_LABEL[status as MetricStatus] ?? status
}

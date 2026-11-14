import type { ReplyAudience } from './types'

/**
 * Tether's proven input attribution on a turn output (docs/reply-audience.md on tether). Each field is a list in
 * the order of input_ids: comma-joined in routed-message metadata, an array in events and in the chat API's meta.
 * Absent or empty means Tether proved nothing; nothing here is ever inferred from the text, the channel or times.
 */
const list = (raw: unknown): string[] =>
  (Array.isArray(raw) ? raw : typeof raw === 'string' ? raw.split(',') : []).map((v) => String(v).trim()).filter(Boolean)

/** Who asked for the turn, from input_classes only: one proven class, else 'unknown'. */
export function audienceOf(inputClasses: unknown): ReplyAudience {
  const classes = list(inputClasses)
  if (classes.includes('operator')) return 'operator'
  return classes.length === 1 && (classes[0] === 'agent' || classes[0] === 'system') ? classes[0] : 'unknown'
}

/**
 * 'mail' when Tether proved the turn was a mail wake (input_sources, CW-20261114-0061): every bound input arrived
 * by the wake path. A turn with no sources is never folded.
 */
export function wakeOf(inputSources: unknown): 'mail' | undefined {
  const sources = list(inputSources)
  return sources.length > 0 && sources.every((s) => s === 'mail_wake') ? 'mail' : undefined
}

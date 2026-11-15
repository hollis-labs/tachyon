import type { ConsoleEvent } from '../events/types'
import type { DeliveryState } from '../messaging/types'
import { observationJSON } from './store'

export interface DeliveryTrace {
  message_id: string
  status?: string
  attempts?: Array<{ stage?: string; turn_submitted_at?: string; consumed_at?: string }>
}

/** Host acceptance alone is not delivery to a runtime. Reading is a separate acknowledgment. */
export function deliveryFromTrace(id: string, trace: DeliveryTrace, fallback: DeliveryState): DeliveryState {
  if (trace.message_id !== id || fallback === 'replied' || fallback === 'read') return fallback
  if (trace.attempts?.some(a => !!a.consumed_at || a.stage === 'consumed')) return 'read'
  if (['dead_letter', 'dead_lettered', 'canceled', 'failed'].includes(trace.status || '')) return 'failed'
  if (trace.attempts?.some(a => !!a.turn_submitted_at || a.stage === 'turn_submitted')) return 'delivered'
  return fallback
}

const EVENT_STATE: Record<string, DeliveryState> = {
  'message.delivered': 'delivered', 'message.read': 'read', 'message.consumed': 'read',
  'message.failed': 'failed', 'message.dead_lettered': 'failed', 'message.canceled': 'failed',
  'message.redriven': 'queued',
}
export function deliveryEvidence(events: ConsoleEvent[], id: string, fallback: DeliveryState) {
  if (fallback === 'replied' || fallback === 'read') return { state: fallback, detail: fallback === 'read' ? 'Recipient acknowledged the message. This does not guarantee understanding.' : 'An explicit reply references this message.' }
  const event = events.filter(e => e.payload.message_id === id && EVENT_STATE[e.kind]).sort((a,b) => b.ts - a.ts || Number(b.cursor) - Number(a.cursor))[0]
  if (!event) return { state: fallback, detail: 'Last known transport status; delivery events are best effort.' }
  const state = EVENT_STATE[event.kind]
  return { state, detail: `${event.kind} recorded at ${new Date(event.ts).toLocaleString()}. Delivery does not mean read.` }
}
export const fetchDeliveryTrace = (id: string) => observationJSON<DeliveryTrace>(`/messages/${encodeURIComponent(id)}/trace`)

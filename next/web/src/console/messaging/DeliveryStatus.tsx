import { useEffect, useState } from 'react'
import { deliveryEvidence } from '../observations/delivery'
import { useObservations } from '../observations/store'
import { useMessaging } from './context'
import { DELIVERY_ORDER, type DeliveryState } from './types'

const STYLE: Record<DeliveryState, { label: string; dot: string; text: string }> = {
  queued: { label: 'QUEUED', dot: 'bg-fg-faint', text: 'text-fg-muted' },
  delivered: { label: 'DELIVERED', dot: 'bg-info', text: 'text-info' },
  read: { label: 'READ', dot: 'bg-success', text: 'text-success' },
  replied: { label: 'REPLIED', dot: 'bg-success', text: 'text-success' },
  failed: { label: 'FAILED', dot: 'bg-danger', text: 'text-danger' },
}

/** Badge for a known state; `steps` adds the queued > delivered > read > replied rail. */
export function DeliveryBadge({ state, steps, detail }: { state: DeliveryState; steps?: boolean; detail?: string }) {
  const s = STYLE[state]
  const reached = DELIVERY_ORDER.indexOf(state)
  return (
    <span className={`inline-flex items-center gap-2 font-mono text-[10px] tracking-widest ${s.text}`} data-state={state} title={detail ?? (state === 'queued' ? 'Queued by the server; recipient delivery is not confirmed.' : state === 'delivered' ? 'Transport delivery confirmed. This does not mean read.' : state === 'read' ? 'Recipient acknowledged the message. This does not guarantee understanding.' : state === 'replied' ? 'An explicit reply references this message.' : 'The transport reported a delivery failure.')}>
      {steps && state !== 'failed' && (
        <span className="inline-flex gap-0.5" aria-hidden>
          {DELIVERY_ORDER.map((d, i) => (
            <span key={d} className={`h-1.5 w-3 ${i <= reached ? s.dot : 'bg-surface'}`} />
          ))}
        </span>
      )}
      {!steps && <span className={`h-1.5 w-1.5 ${s.dot}`} aria-hidden />}
      {s.label}
    </span>
  )
}

/** Events update all visible badges through the shared poller, without per-message requests. */
export function ObservedDeliveryBadge({ messageId, state, steps }: { messageId: string; state: DeliveryState; steps?: boolean }) {
  const observed = useObservations()
  const evidence = deliveryEvidence(observed.events, messageId, state)
  return <DeliveryBadge state={evidence.state} steps={steps} detail={evidence.detail + (observed.error ? ' Activity refresh is unavailable.' : '')} />
}

const TERMINAL: DeliveryState[] = ['replied', 'failed']

/** Polls the client for a sent message until it is replied/failed. */
export function DeliveryStatus({ messageId, initial = 'queued', pollMs = 5000 }: { messageId: string; initial?: DeliveryState; pollMs?: number }) {
  const { client } = useMessaging()
  const [state, setState] = useState<DeliveryState>(initial)
  useEffect(() => {
    let live = true
    let timer: ReturnType<typeof setTimeout> | undefined
    const tick = async () => {
      let next: DeliveryState | null = null
      try {
        next = await client.getDelivery(messageId)
      } catch {
        /* keep last known state and retry */
      }
      if (!live) return
      if (next) setState(next)
      if (!next || !TERMINAL.includes(next)) timer = setTimeout(tick, pollMs)
    }
    void tick()
    return () => {
      live = false
      if (timer) clearTimeout(timer)
    }
  }, [client, messageId, pollMs])
  return <ObservedDeliveryBadge messageId={messageId} state={state} steps />
}

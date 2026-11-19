import { useEffect, useState } from 'react'
import { LinkText } from '../autolink/LinkText'
import { ago } from '@console/lib/agent-activity'
import { useMessaging } from './context'
import { ObservedDeliveryBadge } from './DeliveryStatus'
import type { ThreadMessage, ThreadQuery } from './types'

/** Recent conversation with a recipient (or a thread), oldest first. `refreshKey` re-fetches after a send. */
export function RecentThread({ query, refreshKey = 0, operator }: { query: ThreadQuery; refreshKey?: number; operator?: string }) {
  const { client } = useMessaging()
  const [items, setItems] = useState<ThreadMessage[] | null>(null)
  const [now, setNow] = useState(0)
  const key = `${query.with ?? ''}|${query.threadId ?? ''}|${query.limit ?? ''}`
  useEffect(() => {
    let live = true
    client.recentThread(query).then(
      (m) => live && (setNow(Date.now()), setItems(m)),
      () => live && setItems([]),
    )
    return () => {
      live = false
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [client, key, refreshKey])

  return (
    <section aria-label="Recent thread">
      <h3 className="mb-2 font-mono text-[10px] uppercase tracking-widest text-fg-muted">Recent thread</h3>
      {items === null ? (
        <p className="font-mono text-xs text-fg-muted">Loading</p>
      ) : items.length === 0 ? (
        <p className="font-mono text-xs text-fg-muted">No messages yet.</p>
      ) : (
        <ul className="divide-y divide-border-subtle border-y border-border-subtle">
          {items.map((m) => {
            const mine = !operator || m.from === operator || m.from.startsWith('msg://user/')
            return (
              <li key={m.id} className="px-3 py-2">
                <div className="flex items-center justify-between gap-3 font-mono text-[10px] uppercase tracking-widest text-fg-muted">
                  <span>{mine ? 'you' : 'them'}{m.intent !== 'message' ? ` · ${m.intent.replace('_', ' ')}` : ''} · {ago(m.at, now)}</span>
                  {mine && <ObservedDeliveryBadge messageId={m.id} state={m.state} />}
                </div>
                <p className="mt-1 whitespace-pre-wrap font-mono text-xs text-fg"><LinkText surface="messaging" text={m.body} /></p>
              </li>
            )
          })}
        </ul>
      )}
    </section>
  )
}

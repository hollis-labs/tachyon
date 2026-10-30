import { MetaList } from '@hollis-labs/design-components'
import { CapabilityBadge } from '../CapabilityBadge'
import type { ChatThread } from '../types'

const when = (at: number) => (at > 0 ? new Date(at).toLocaleString() : 'Not yet')

const AVAILABLE = ['Send and read messages', 'Replies filter (all, or only from the agent)', 'Forward, copy and download a message', 'Message info', 'Tool chips on agent replies', 'Drafts kept per conversation', 'Unread counts', 'Reachability and freshness']
const WAITING = ['Live streaming of replies', 'Stop or interrupt a reply in progress', 'Unread and the thread list kept on the server', 'Markdown in messages']

function Features({ title, items }: { title: string; items: string[] }) {
  return (
    <section aria-label={title} className="space-y-1">
      <h3 className="font-mono dash-caption uppercase tracking-label text-fg-faint">{title}</h3>
      <ul className="space-y-0.5 text-caption text-fg-muted">{items.map((i) => <li key={i}>{i}</li>)}</ul>
    </section>
  )
}

/** The right column: what we know about the open thread, and which chat features exist. Tabs and tools come later. */
export function ThreadDetails({ thread }: { thread: ChatThread | null }) {
  if (!thread) return <p className="px-4 py-4 text-caption text-fg-faint">Open a conversation to see its details.</p>
  return (
    <div className="space-y-4 px-4 py-4">
      <MetaList
        columns={1}
        items={[
          { label: 'Title', value: thread.title },
          { label: 'Agent', value: thread.agent.label },
          { label: 'Reachability', value: <CapabilityBadge capability={thread.capability} withHint /> },
          { label: 'Last activity', value: when(thread.lastAt) },
          { label: 'Thread', value: <span className="break-all font-mono dash-caption">{thread.id}</span> },
        ]}
      />
      <Features title="Chat can" items={AVAILABLE} />
      <Features title="Waiting on the backend" items={WAITING} />
    </div>
  )
}

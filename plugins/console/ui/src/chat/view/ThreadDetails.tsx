import { MetaList } from '@hollis-labs/design-components'
import { CapabilityBadge } from '../CapabilityBadge'
import type { ChatThread } from '../types'

const when = (at: number) => (at > 0 ? new Date(at).toLocaleString() : 'Not yet')

/** Stub for the right column: what we know about the open thread. Tabs and tools come later. */
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
    </div>
  )
}

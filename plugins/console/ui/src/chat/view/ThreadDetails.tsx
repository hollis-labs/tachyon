import { MetaList } from '@hollis-labs/design-components'
import { CapabilityBadge } from '../CapabilityBadge'
import type { ChatThread } from '../types'
import { type ThreadContext, useThreadContext } from './thread-context'

const when = (at: number) => (at > 0 ? new Date(at).toLocaleString() : 'Not yet')
const ago = (at: number, now = Date.now()) => {
  const s = Math.max(0, Math.round((now - at) / 1000))
  return s < 60 ? 'just now' : s < 3600 ? `${Math.floor(s / 60)}m ago` : s < 86_400 ? `${Math.floor(s / 3600)}h ago` : `${Math.floor(s / 86_400)}d ago`
}
const size = (b?: number | null) => (b == null ? '' : b < 1024 ? `${b} B` : b < 1_048_576 ? `${Math.round(b / 1024)} KB` : `${(b / 1_048_576).toFixed(1)} MB`)

function Section({ title, empty, children }: { title: string; empty: string; children: React.ReactNode[] }) {
  return (
    <section aria-label={title} className="space-y-1.5">
      <h3 className="font-mono dash-caption uppercase tracking-label text-fg-faint">{title}</h3>
      {children.length === 0 ? <p className="text-caption text-fg-muted">{empty}</p> : <ul className="space-y-1.5 text-caption text-fg">{children}</ul>}
    </section>
  )
}

function Context({ ctx }: { ctx: ThreadContext }) {
  if (ctx.state === 'loading') return <p role="status" className="text-caption text-fg-muted">Loading context…</p>
  if (ctx.state === 'unavailable') return <p role="status" className="text-caption text-fg-muted">Context is not available right now.</p>
  return (
    <>
      <Section title="Activity" empty="No activity in this thread yet.">
        {ctx.activity.map((a) => (
          <li key={a.seq} className="flex flex-col">
            <span>{a.title ?? a.type}</span>
            <span className="text-fg-muted">{[a.summary, ago(a.at)].filter(Boolean).join(' · ')}</span>
          </li>
        ))}
      </Section>
      <Section title="Files" empty="No files shared in this thread.">
        {ctx.files.map((f) => (
          <li key={f.id} className="flex justify-between gap-2">
            <span className="min-w-0 truncate">{f.name}</span>
            <span className="shrink-0 text-fg-muted">{size(f.sizeBytes)}</span>
          </li>
        ))}
      </Section>
      <Section title="Linked work" empty="No tasks or plan items linked to this thread.">
        {ctx.work.map((w) => (
          <li key={w.id} className="flex flex-col">
            <span>{w.title}</span>
            <span className="text-fg-muted">{[w.kind, w.status, w.needsUser ? 'needs you' : ''].filter(Boolean).join(' · ')}</span>
          </li>
        ))}
      </Section>
    </>
  )
}

/** The right column: the thread, then its real context (activity, files, linked work) from the chat API. */
export function ThreadDetails({ thread }: { thread: ChatThread | null }) {
  const ctx = useThreadContext(thread)
  if (!thread) return <p className="px-4 py-4 text-caption text-fg-faint">Open a conversation to see its details.</p>
  return (
    <div className="space-y-5 px-4 py-4">
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
      <Context ctx={ctx} />
    </div>
  )
}

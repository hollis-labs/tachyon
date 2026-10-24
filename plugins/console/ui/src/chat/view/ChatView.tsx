import { useState, type ReactNode } from 'react'
import { Button, EmptyState, OverlaySidebar, type OverflowAction } from '@hollis-labs/design-components'
import { PanelLeft, PanelLeftClose, PanelRight, PanelRightClose } from 'lucide-react'
import { ChatFreshness } from '../ChatFreshness'
import type { ChatThread } from '../types'
import { ThreadDetails } from './ThreadDetails'
import { ThreadList } from './ThreadList'
import { useNarrow } from './useNarrow'

export interface ChatViewProps {
  threads: ChatThread[] | null
  threadsError?: string | null
  onRetryThreads?: () => void
  activeThread: ChatThread | null
  onSelectThread: (id: string) => void
  newChatActions?: OverflowAction[]
  /**
   * 'page' (default): threads | conversation | details. 'compact': one column for a
   * drawer, showing the thread list or the open conversation with a back button.
   */
  variant?: 'page' | 'compact'
  /** Compact only: return from the conversation to the thread list. */
  onBack?: () => void
  /** The conversation for `activeThread`. Rendered in the centre column. */
  children?: ReactNode
}

/**
 * Three columns: threads, conversation, details. The host owns the data, the
 * selected thread and the conversation (children). Below 1024px the side
 * columns open as overlays; the details column starts collapsed.
 */
export function ChatView({ threads, threadsError, onRetryThreads, activeThread, onSelectThread, newChatActions, variant = 'page', onBack, children }: ChatViewProps) {
  const narrow = useNarrow()
  const [threadsOpen, setThreadsOpen] = useState(true)
  const [detailsOpen, setDetailsOpen] = useState(false)
  const [threadsOverlay, setThreadsOverlay] = useState(false)
  const [detailsOverlay, setDetailsOverlay] = useState(false)

  const list = (afterSelect?: () => void) => (
    <ThreadList
      threads={threads}
      activeId={activeThread?.id ?? null}
      onSelect={(id) => { onSelectThread(id); afterSelect?.() }}
      newChatActions={newChatActions}
      error={threadsError}
      onRetry={onRetryThreads}
    />
  )

  if (variant === 'compact') {
    return (
      <div className="flex min-h-0 flex-1 flex-col bg-bg">
        <ChatFreshness />
        <header className="flex shrink-0 items-center gap-3 border-b border-border px-3 py-2">
          {activeThread && onBack && <button type="button" onClick={onBack} aria-label="Back to threads" className="font-mono text-xs text-fg-muted hover:text-fg-secondary focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring">&lt; Threads</button>}
          <h2 className="min-w-0 flex-1 truncate text-sm font-normal normal-case tracking-normal text-fg">{activeThread?.title ?? 'Chat'}</h2>
        </header>
        {activeThread ? children : list()}
      </div>
    )
  }

  const iconBtn = 'shrink-0 text-fg-muted'
  const threadsToggle = narrow ? (
    <OverlaySidebar
      open={threadsOverlay}
      onOpenChange={setThreadsOverlay}
      side="left"
      title="Conversations"
      trigger={<Button type="button" variant="ghost" size="sm" aria-label="Show conversations" className={iconBtn}><PanelLeft className="size-4" aria-hidden="true" /></Button>}
    >
      {list(() => setThreadsOverlay(false))}
    </OverlaySidebar>
  ) : (
    <Button type="button" variant="ghost" size="sm" aria-label={threadsOpen ? 'Hide conversations' : 'Show conversations'} aria-expanded={threadsOpen} onClick={() => setThreadsOpen((o) => !o)} className={iconBtn}>
      {threadsOpen ? <PanelLeftClose className="size-4" aria-hidden="true" /> : <PanelLeft className="size-4" aria-hidden="true" />}
    </Button>
  )
  const detailsToggle = narrow ? (
    <OverlaySidebar
      open={detailsOverlay}
      onOpenChange={setDetailsOverlay}
      side="right"
      title="Thread details"
      trigger={<Button type="button" variant="ghost" size="sm" aria-label="Show thread details" className={iconBtn}><PanelRight className="size-4" aria-hidden="true" /></Button>}
    >
      <ThreadDetails thread={activeThread} />
    </OverlaySidebar>
  ) : (
    <Button type="button" variant="ghost" size="sm" aria-label={detailsOpen ? 'Hide thread details' : 'Show thread details'} aria-expanded={detailsOpen} onClick={() => setDetailsOpen((o) => !o)} className={iconBtn}>
      {detailsOpen ? <PanelRightClose className="size-4" aria-hidden="true" /> : <PanelRight className="size-4" aria-hidden="true" />}
    </Button>
  )

  return (
    <div className="flex h-full min-h-0 bg-bg">
      {!narrow && threadsOpen && (
        <aside aria-label="Conversations" className="flex w-72 shrink-0 flex-col border-r border-border">
          {list()}
        </aside>
      )}
      <section aria-label="Chat" className="flex min-w-0 flex-1 flex-col">
        <header className="flex shrink-0 items-center gap-2 border-b border-border px-3 py-2">
          {threadsToggle}
          <h1 className="min-w-0 flex-1 truncate text-control font-normal normal-case tracking-normal text-fg">{activeThread?.title ?? 'Chat'}</h1>
          {detailsToggle}
        </header>
        <ChatFreshness />
        {activeThread ? children : (
          <div className="flex min-h-0 flex-1 items-center justify-center">
            <EmptyState variant="empty" title="Select a conversation" description="Pick one from the list, or start a new chat with an agent." />
          </div>
        )}
      </section>
      {!narrow && detailsOpen && (
        <aside aria-label="Thread details" className="w-96 shrink-0 overflow-y-auto border-l border-border">
          <ThreadDetails thread={activeThread} />
        </aside>
      )}
    </div>
  )
}

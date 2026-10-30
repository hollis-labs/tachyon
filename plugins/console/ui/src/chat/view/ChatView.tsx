import { useEffect, useState, type ReactNode } from 'react'
import { EmptyState, Sheet, SheetContent, SheetTitle, type OverflowAction } from '@hollis-labs/design-components'
import { ChatFreshness } from '../ChatFreshness'
import type { ChatThread } from '../types'
import { ThreadDetails } from './ThreadDetails'
import { ThreadList } from './ThreadList'
import { Header } from '../workbench/Header'
import { Spine, type StartOption } from '../workbench/Spine'
import { useNarrow } from './useNarrow'

export interface ChatViewProps {
  threads: ChatThread[] | null
  threadsError?: string | null
  onRetryThreads?: () => void
  activeThread: ChatThread | null
  onSelectThread: (id: string) => void
  /** Open one of the agent's other sessions in the conversation. */
  onSelectSession?: (ref: string) => void
  newChatActions?: OverflowAction[]
  /** Agents a chat can start with (the Start menu, ⌘N). */
  startOptions?: StartOption[]
  onTogglePin?: () => void
  /**
   * 'page' (default): threads | conversation | details. 'compact': one column for a
   * drawer, showing the thread list or the open conversation with a back button.
   */
  variant?: 'page' | 'compact'
  /** Compact, and the page below 1024px: return from the conversation to the thread list. */
  onBack?: () => void
  /** The conversation for `activeThread`. Rendered in the centre column. */
  children?: ReactNode
}

/**
 * Three columns: threads, conversation, details. The host owns the data, the
 * selected thread and the conversation (children). Below 1024px the list is
 * the home screen (a conversation opens full width with a back control) and the details open as a bottom
 * sheet; the details column starts open from 1280px up.
 */
export function ChatView({ threads, threadsError, onRetryThreads, activeThread, onSelectThread, onSelectSession, newChatActions, startOptions, onTogglePin, variant = 'page', onBack, children }: ChatViewProps) {
  const narrow = useNarrow()
  // Open from 1280px up so the column is seen; below that it stays collapsed (overlay under 1024px).
  const [detailsOpen] = useState(() => typeof window.matchMedia === 'function' && window.matchMedia('(min-width: 1280px)').matches)
  const [detailsOverlay, setDetailsOverlay] = useState(false)

  // Esc returns to the list on a narrow screen, unless it is meant for a field or an open sheet.
  const inThread = narrow && !!activeThread && !!onBack
  useEffect(() => {
    if (!inThread) return
    const on = (e: KeyboardEvent) => {
      const t = e.target as HTMLElement | null
      if (e.key !== 'Escape' || e.defaultPrevented || detailsOverlay || t?.closest('input, textarea, [contenteditable], [role=dialog]')) return
      onBack?.()
    }
    window.addEventListener('keydown', on)
    return () => window.removeEventListener('keydown', on)
  }, [inThread, onBack, detailsOverlay])

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

  const spine = (
    <Spine threads={threads} activeId={activeThread?.id ?? null} onSelect={onSelectThread} startOptions={startOptions} error={threadsError} onRetry={onRetryThreads} />
  )
  if (variant === 'compact') {
    return (
      // relative + overflow-hidden is load-bearing: the message action buttons contain
      // position:absolute sr-only labels. With no positioned, clipping ancestor they escape the
      // message scroller and stretch the whole page's scroll height.
      <div className="relative flex min-h-0 flex-1 flex-col overflow-hidden bg-bg">
        <ChatFreshness />
        <header className="flex shrink-0 items-center gap-3 border-b border-border px-3 py-2">
          {activeThread && onBack && <button type="button" onClick={onBack} aria-label="Back to threads" className="font-mono text-xs text-fg-muted hover:text-fg-secondary focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring">&lt; Threads</button>}
          <h2 className="min-w-0 flex-1 truncate text-sm font-normal normal-case tracking-normal text-fg">{activeThread?.title ?? 'Chat'}</h2>
        </header>
        {activeThread ? children : list()}
      </div>
    )
  }

  // Below 1024px the conversation list is the home screen: no empty pane, no hidden toggle to find.
  if (narrow && !activeThread) {
    return (
      <div className="relative flex h-full min-h-0 flex-col overflow-hidden bg-bg">
        <ChatFreshness />
        {spine}
      </div>
    )
  }

  return (
    <div className="relative flex h-full min-h-0 overflow-hidden bg-bg">
      {!narrow && <div className="flex w-72 shrink-0 border-r border-border-subtle">{spine}</div>}
      <main aria-label="Conversation" className="flex min-w-0 flex-1 flex-col">
        {activeThread ? (
          <Header thread={activeThread} onSelectSession={onSelectSession} onTogglePin={onTogglePin} onBack={narrow ? onBack : undefined} onDetails={narrow ? () => setDetailsOverlay(true) : undefined} detailsOpen={detailsOverlay} />
        ) : (
          <header className="h-12 shrink-0 border-b border-border-subtle" />
        )}
        <ChatFreshness />
        {activeThread ? children : (
          <div className="flex min-h-0 flex-1 items-center justify-center">
            <EmptyState variant="empty" title="Select a chat" description="Pick one from the list, or start a chat with an agent." />
          </div>
        )}
      </main>
      {narrow ? (
        <Sheet open={detailsOverlay} onOpenChange={setDetailsOverlay}>
          <SheetContent side="bottom" className="max-h-[72vh] overflow-y-auto">
            <SheetTitle className="px-4 pt-4">Details</SheetTitle>
            <ThreadDetails thread={activeThread} onSelectSession={onSelectSession} />
          </SheetContent>
        </Sheet>
      ) : (
        detailsOpen && (
          // Interim: slice C-D replaces this static list with the aside host and its panels.
          <aside aria-label="Details" className="w-80 shrink-0 overflow-y-auto border-l border-border-subtle bg-bg-elevated">
            <ThreadDetails thread={activeThread} onSelectSession={onSelectSession} />
          </aside>
        )
      )}
    </div>
  )
}

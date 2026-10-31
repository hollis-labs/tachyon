import { useEffect, useState, type ReactNode } from 'react'
import { EmptyState, type OverflowAction } from '@hollis-labs/design-components'
import { Aside } from '../aside/Aside'
import { AsideProvider, useAside } from '../aside/context'
import { setScratchPlacement, useScratchPlacement } from '../drawer/model'
import { ChatFreshness } from '../ChatFreshness'
import type { ChatThread } from '../types'
import { ThreadList } from './ThreadList'
import { Header } from '../workbench/Header'
import { Spine, type StartOption } from '../workbench/Spine'
import { useMobile, useNarrow } from './useNarrow'
import { HeaderFilterContext } from './headerFilter'
import { useThreadFrames } from '../live'
import { useReplyFilter } from '../replyFilter'

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
export function ChatView(props: ChatViewProps) {
  const wide = typeof window !== 'undefined' && typeof window.matchMedia === 'function' && window.matchMedia('(min-width: 1280px)').matches
  return (
    <AsideProvider wide={wide}>
      <ChatViewInner {...props} />
    </AsideProvider>
  )
}

function ChatViewInner({ threads, threadsError, onRetryThreads, activeThread, onSelectThread, onSelectSession, newChatActions, startOptions, onTogglePin, variant = 'page', onBack, children }: ChatViewProps) {
  const narrow = useNarrow()
  const mobile = useMobile()
  const aside = useAside()
  // A `context` frame carries the live window use; it updates the header meter without waiting for a list refresh.
  const [liveCtx, setLiveCtx] = useState<{ ref: string; used: number | null; window: number | null; model: string | null } | null>(null)
  const ctxRef = activeThread ? (activeThread.sessionRef ?? activeThread.sessions?.[0]?.ref) : undefined
  useThreadFrames(ctxRef, (f) => {
    if (f.type === 'context' && ctxRef) setLiveCtx({ ref: ctxRef, used: typeof f.used === 'number' ? f.used : null, window: typeof f.window === 'number' ? f.window : null, model: typeof f.model === 'string' ? f.model : null })
  })
  const [replyFilter, setReplyFilter] = useReplyFilter()
  const scratchPlacement = useScratchPlacement()
  const detailsOverlay = aside.open

  // Esc returns to the list on a narrow screen, unless it is meant for a field or an open sheet.
  const inThread = mobile && !!activeThread && !!onBack
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
  if (mobile && !activeThread) {
    return (
      <div className="relative flex h-full min-h-0 flex-col overflow-hidden bg-bg">
        <ChatFreshness />
        {spine}
      </div>
    )
  }

  return (
    <div className="relative flex h-full min-h-0 overflow-hidden bg-bg">
      {!mobile && <div className={`flex shrink-0 border-r border-border-subtle ${narrow ? 'w-60' : 'w-72'}`}>{spine}</div>}
      <HeaderFilterContext.Provider value={true}>
      <main aria-label="Conversation" className="flex min-w-0 flex-1 flex-col">
        {activeThread ? (
          <Header thread={liveCtx && liveCtx.ref === ctxRef ? { ...activeThread, context: { used: liveCtx.used, window: liveCtx.window, model: liveCtx.model ?? activeThread.context?.model ?? null } } : activeThread} onSelectSession={onSelectSession} onTogglePin={onTogglePin} onBack={mobile ? onBack : undefined} onDetails={narrow ? aside.toggle : undefined} detailsOpen={detailsOverlay} actions={[{ label: replyFilter === 'mine' ? 'Show all replies' : 'Only show replies to me', onSelect: () => setReplyFilter(replyFilter === 'mine' ? 'all' : 'mine') }, { label: scratchPlacement === 'aside' ? 'Open scratchpad in the drawer' : 'Open scratchpad in the aside', onSelect: () => void setScratchPlacement(scratchPlacement === 'aside' ? 'drawer' : 'aside').catch(() => undefined) }, { label: 'Copy link to this chat', onSelect: () => void navigator.clipboard?.writeText(location.href) }]} />
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
      </HeaderFilterContext.Provider>
      {activeThread && <Aside thread={activeThread} scratchInAside={scratchPlacement === 'aside'} sessionRef={activeThread.sessionRef ?? activeThread.sessions?.[0]?.ref} />}
    </div>
  )
}

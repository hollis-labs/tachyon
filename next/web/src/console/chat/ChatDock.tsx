import { ApprovalTrigger } from '../approvals/page'
import { useEffect, useRef } from 'react'
import type { Recipient } from '../messaging'
import { useRestoreFocus } from '../shell/restore-focus'
import { ChatPanel } from './ChatPanel'

/**
 * The chat dock's body, hosting ChatPanel. It renders inside the app-shell dock (region `chat.dock`), which owns
 * open state, size, Escape and Close; the host passes `open`.
 */
export function ChatDock({ open, onClose, agent }: { open: boolean; onClose: () => void; agent?: Recipient | null }) {
  const ref = useRef<HTMLElement>(null)
  useRestoreFocus(open, '[data-chat-trigger]')
  useEffect(() => {
    if (!open) return
    ref.current?.focus()
    // Lets the alert toast stack slide left instead of covering the drawer (see AlertToastStack).
    document.documentElement.dataset.chatOpen = ''
    return () => void delete document.documentElement.dataset.chatOpen
  }, [open])
  if (!open) return null
  return (
    <section ref={ref} tabIndex={-1} aria-label="Chat drawer" className="flex h-full min-h-0 flex-col focus-visible:outline-none">
      <div className="flex shrink-0 items-center justify-between border-b border-border px-3 py-1">
        <ApprovalTrigger onNavigate={onClose} />
      </div>
      <ChatPanel agent={agent} className="flex-1" />
    </section>
  )
}

/** Compact trigger with unread badge, for agent rows and the console header. */
export function ChatButton({ onClick, unread = 0, label = 'Chat' }: { onClick: () => void; unread?: number; label?: string }) {
  return (
    <button type="button" onClick={onClick} className="inline-flex items-center gap-2 border border-border px-2 py-1 font-mono dash-label uppercase tracking-widest text-fg-secondary hover:text-fg focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring">
      {label}
      {unread > 0 && <span aria-label={`${unread} unread`} className="border border-primary/60 px-1 text-primary">{unread}</span>}
    </button>
  )
}

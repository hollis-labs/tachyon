import { ApprovalTrigger } from '../approvals/page'
import { useEffect, useRef, useState } from 'react'
import { ResizableChatPanel } from '@hollis-labs/kit-chat'
import { Button } from '@hollis-labs/design-components'
import type { Recipient } from '../messaging'
import { useRestoreFocus } from '../shell/restore-focus'
import { ChatPanel } from './ChatPanel'

/** Right-docked drawer hosting ChatPanel. The host owns `open`; Escape closes. */
export function ChatDock({ open, onClose, agent }: { open: boolean; onClose: () => void; agent?: Recipient | null }) {
  const ref = useRef<HTMLElement>(null)
  const [width, setWidth] = useState(() => {
    try { const saved = Number(localStorage.getItem('tachyon.chat.width')); return saved >= 360 && saved <= 960 ? saved : 480 } catch { return 480 }
  })
  useEffect(() => {
    try { localStorage.setItem('tachyon.chat.width', String(width)) } catch { /* storage is optional */ }
    if (open) document.documentElement.style.setProperty('--tachyon-chat-width', `${width}px`)
    return () => { document.documentElement.style.removeProperty('--tachyon-chat-width') }
  }, [width, open])
  useRestoreFocus(open, '[data-chat-trigger]')
  useEffect(() => {
    if (!open) return
    ref.current?.focus()
    // Lets the alert toast stack slide left instead of covering the drawer (see AlertToastStack).
    document.documentElement.dataset.chatOpen = ''
    return () => void delete document.documentElement.dataset.chatOpen
  }, [open])
  useEffect(() => {
    if (!open) return
    // Another modal on top (alert detail) owns Escape.
    const onKey = (e: KeyboardEvent) => e.key === 'Escape' && !e.defaultPrevented && !document.querySelector('[aria-modal="true"]') && onClose()
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [open, onClose])
  if (!open) return null
  return (
    <ResizableChatPanel ref={ref} width={width} onWidthChange={setWidth} tabIndex={-1} aria-label="Chat drawer" className="fixed inset-y-0 right-0 z-40 focus-visible:outline-none">
      <div className="flex shrink-0 items-center justify-between border-b border-border px-3 py-1">
        <ApprovalTrigger onNavigate={onClose} />
        <Button type="button" variant="ghost" size="sm" onClick={onClose} aria-label="Close chat" className="font-mono dash-caption uppercase tracking-widest text-fg-muted">Close</Button>
      </div>
      <ChatPanel agent={agent} className="flex-1" />
    </ResizableChatPanel>
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

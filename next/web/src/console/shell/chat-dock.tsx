import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { useActionGateway } from '@hollis-labs/app-shell'
import { MessageSquare } from 'lucide-react'
import type { Recipient } from '../messaging'
import { ChatDock } from '../chat/ChatDock'
import { useChatUnread } from '../chat/hooks'

/** The app-shell docked region and its tab (next/cmd/tachyon/substrate.yaml frontend.regions, ui/app.json). */
export const CHAT_DOCK_REGION = 'chat.dock'
export const CHAT_DOCK_TAB = 'chat-dock'

interface ChatDockApi {
  open: boolean
  /** Open the dock, optionally straight into one agent's conversation. */
  openChat: (agent?: Recipient | null) => void
  closeChat: () => void
}

/** The shell's side of the dock: open or close its region, through the action gateway. */
interface DockRegion { open: () => void; close: () => void }
interface DockState { agent: Recipient | null; closeChat: () => void; shown: (on: boolean) => void; bind: (region: DockRegion | null) => void }

const ChatDockContext = createContext<ChatDockApi | null>(null)
const DockStateContext = createContext<DockState | null>(null)

/**
 * Owns the dock's recipient and open state. The app-shell renders the dock (dock.tab `chat-dock`, ChatDockTab) and
 * opens it from the header toolbar; openChat opens it through the action gateway (ChatDockGateway). Mount inside
 * ChatProvider, above ManifestShell.
 */
export function ChatDockProvider({ children }: { children: ReactNode }) {
  const [state, setState] = useState<{ open: boolean; agent: Recipient | null }>({ open: false, agent: null })
  const region = useRef<DockRegion | null>(null)
  const openChat = useCallback((agent?: Recipient | null) => {
    setState((s) => ({ ...s, agent: agent ?? null }))
    region.current?.open()
  }, [])
  const closeChat = useCallback(() => region.current?.close(), [])
  // Closed by any means (Esc, the shell's Close, closeChat), the dock forgets its recipient: the toolbar then opens
  // the conversation list, as the old trigger's openChat() did.
  const shown = useCallback((on: boolean) => setState((s) => (on ? { ...s, open: true } : { open: false, agent: null })), [])
  const bind = useCallback((r: DockRegion | null) => { region.current = r }, [])
  const api = useMemo<ChatDockApi>(() => ({ open: state.open, openChat, closeChat }), [state.open, openChat, closeChat])
  const dock = useMemo<DockState>(() => ({ agent: state.agent, closeChat, shown, bind }), [state.agent, closeChat, shown, bind])
  return (
    <ChatDockContext.Provider value={api}>
      <DockStateContext.Provider value={dock}>{children}</DockStateContext.Provider>
    </ChatDockContext.Provider>
  )
}

/** Binds openChat / closeChat to the shell's action gateway (openRegion / closeRegion). Mount inside ManifestShell. */
export function ChatDockGateway() {
  const gateway = useActionGateway()
  const bind = useContext(DockStateContext)?.bind
  useEffect(() => {
    if (!bind) return
    let seq = 0
    const call = (fn: 'openRegion' | 'closeRegion') => {
      void gateway.handleCall(
        {
          surfaceId: `tachyon.${CHAT_DOCK_TAB}`,
          functionCallId: `${CHAT_DOCK_TAB}.${fn}.${++seq}`,
          callFunction: { '@call': fn, region: CHAT_DOCK_REGION, ...(fn === 'openRegion' ? { tab: CHAT_DOCK_TAB } : {}) },
        },
        { trigger: document.activeElement },
      )
    }
    bind({ open: () => call('openRegion'), close: () => call('closeRegion') })
    return () => bind(null)
  }, [gateway, bind])
  return null
}

/** The `chat-dock` dock.tab's view: the drawer, inside the shell's dock. It tells the provider while it shows. */
export function ChatDockTab() {
  const dock = useContext(DockStateContext)
  const shown = dock?.shown
  const mounts = useRef(0)
  useEffect(() => {
    if (!shown) return
    const n = ++mounts.current
    shown(true)
    // Deferred: a StrictMode remount follows at once, and is not a close.
    return () => void setTimeout(() => mounts.current === n && shown(false), 0)
  }, [shown])
  if (!dock) return null
  return <ChatDock open onClose={dock.closeChat} agent={dock.agent} />
}

/** Null outside an enabled Console, so callers can render nothing. */
// eslint-disable-next-line react-refresh/only-export-components
export function useOptionalChatDock(): ChatDockApi | null {
  return useContext(ChatDockContext)
}

/** Nav-rail trigger (h-9 w-9 like the bell). Renders nothing when the Console is off. */
export function ChatDockTrigger() {
  const dock = useOptionalChatDock()
  if (!dock) return null
  return <ChatTrigger dock={dock} />
}

function ChatTrigger({ dock }: { dock: ChatDockApi }) {
  const unread = useChatUnread()
  const shown = unread > 99 ? '99+' : String(unread)
  return (
    <button
      type="button"
      data-chat-trigger=""
      aria-label={unread > 0 ? `Chat, ${shown} unread` : 'Chat'}
      aria-haspopup="dialog"
      aria-expanded={dock.open}
      onClick={() => (dock.open ? dock.closeChat() : dock.openChat())}
      className={`relative flex h-9 w-9 items-center justify-center text-muted-foreground transition-colors hover:bg-accent hover:text-foreground focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-warning ${dock.open ? 'bg-accent text-foreground' : ''}`}
    >
      <MessageSquare aria-hidden="true" className="h-4 w-4" />
      {unread > 0 && <span aria-hidden="true" className="absolute -right-0.5 -top-0.5 min-w-[14px] border border-warning/50 bg-bg px-1 text-center font-mono text-[9px] leading-[12px] text-warning">{shown}</span>}
    </button>
  )
}

/** Bell + chat for the nav rail footer. */
export function ConsoleNavExtras({ alerts }: { alerts: ReactNode }) {
  return (
    <>
      {alerts}
      <ChatDockTrigger />
    </>
  )
}

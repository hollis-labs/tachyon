import { useEffect, useState } from 'react'
import { AgentPresence } from '../observations/AgentPresence'
import { Mail, MessageSquare } from 'lucide-react'
import { ago } from '@console/lib/agent-activity'
import type { Recipient } from '../messaging'
import { ChatFreshness } from './ChatFreshness'
import { CapabilityBadge } from './CapabilityBadge'
import { ChatComposer } from './ChatComposer'
import { useChat } from './context'
import { useChatThread, useChatThreads } from './hooks'
import { ThreadView } from './ThreadView'
import type { ChatThread } from './types'

function ThreadRow({ t, now, onOpen }: { t: ChatThread; now: number; onOpen: () => void }) {
  return (
    <li className="border-b border-border/80">
      <button type="button" onClick={onOpen} className="block w-full px-3 py-2 text-left hover:bg-surface/60 focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-inset focus-visible:ring-ring">
        <div className="flex items-center justify-between gap-3">
          <span className="flex min-w-0 items-center gap-2 font-mono text-xs text-fg">{t.capability.mode === 'session' || t.capability.mode === 'live' ? <MessageSquare aria-label="Chat" className="h-3.5 w-3.5 shrink-0 text-success" /> : <Mail aria-label="Email" className="h-3.5 w-3.5 shrink-0 text-info" />}<span className="truncate">{t.title}</span></span>
          {t.unread > 0 && <span aria-label={`${t.unread} unread`} className="border border-primary/60 px-1.5 font-mono dash-caption text-primary">{t.unread}</span>}
        </div>
        <div className="mt-1 flex items-center justify-between gap-3">
          <CapabilityBadge capability={t.capability} />
          {t.lastAt > 0 && <span className="font-mono dash-caption text-fg-faint">{ago(t.lastAt, now)}</span>}
        </div>
        {t.lastPreview && <p className="mt-1 truncate font-mono dash-label text-fg-faint">{t.lastPreview}</p>}
      </button>
    </li>
  )
}

function Conversation({ thread, onBack }: { thread: ChatThread; onBack?: () => void }) {
  const { messages, capability, loading, hasMore, loadOlder, send, sendError, error } = useChatThread(thread.id)
  const cap = capability ?? thread.capability
  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <header className="space-y-1 border-b border-border px-3 py-2">
        <div className="flex items-center gap-3">
          {onBack && <button type="button" onClick={onBack} aria-label="Back to threads" className="font-mono text-xs text-fg-muted hover:text-fg-secondary focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring">&lt; Threads</button>}
          <h2 className="truncate font-mono text-sm text-fg">{thread.title}</h2>
        </div>
        <CapabilityBadge capability={cap} withHint />
        <AgentPresence sessionId={cap.sessionId} address={thread.agent.address} />
      </header>
      {error && <p role="alert" className="border-b border-danger/30 px-3 py-2 font-mono text-xs text-danger">{error}</p>}
      <ThreadView messages={messages} agentLabel={thread.title} loading={loading} hasMore={hasMore} onLoadOlder={() => void loadOlder()} />
      <ChatComposer onSend={send} capability={error ? { ...cap, canSend: false } : cap} error={sendError} />
    </div>
  )
}

export interface ChatPanelProps {
  /** Jump straight into the conversation with this agent (from an agent row or alert). */
  agent?: Recipient | null
  className?: string
}

/** Thread list plus conversation. Fits a drawer; stateless apart from the open thread. */
export function ChatPanel({ agent, className }: ChatPanelProps) {
  const { client } = useChat()
  const {threads,error:threadsError,retry} = useChatThreads()
  const [now] = useState(() => Date.now())
  const [openId, setOpenId] = useState<string | null>(null)
  const [openVersion, setOpenVersion] = useState(0)
  const [openError,setOpenError] = useState<string | null>(null)
  const [opened, setOpened] = useState<ChatThread | null>(null)
  const addr = agent?.address
  useEffect(() => {
    if (!agent) return
    let live = true
    client.openThread(agent).then((t) => {
      if (live) {
        setOpenError(null)
        setOpened(t)
        setOpenId(t.id)
      }
    }, e => {if(live) setOpenError(e instanceof Error ? e.message : "Could not open conversation")})
    return () => {
      live = false
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [client, addr, openVersion])

  const active = openId ? (threads?.find((t) => t.id === openId) ?? (opened?.id === openId ? opened : null)) : null
  return (
    <section aria-label="Chat" className={`flex min-h-0 flex-col bg-bg ${className ?? ''}`}>
      <ChatFreshness />
      {active ? (
        <Conversation key={active.id} thread={active} onBack={() => setOpenId(null)} />
      ) : (
        <>
          <header className="border-b border-border px-3 py-2">
            <h2 className="font-mono text-sm text-fg">Chat</h2>
          </header>
          {(threadsError || openError) && <div role="alert" className="px-3 py-3 text-xs text-danger">{threadsError || openError}<button type="button" onClick={() => {retry();setOpenVersion(v => v + 1)}} className="ml-2 underline">Retry</button></div>}
          {threads === null ? (
            <p className="px-3 py-4 font-mono text-xs text-fg-faint">{threadsError ? "Conversation list unavailable" : "Loading"}</p>
          ) : threads.length === 0 ? (
            <p className="px-3 py-4 font-mono text-xs text-fg-faint">No conversations. Open one from an agent.</p>
          ) : (
            <ul aria-label="Threads" className="overflow-y-auto">{threads.map((t) => <ThreadRow key={t.id} t={t} now={now} onOpen={() => setOpenId(t.id)} />)}</ul>
          )}
        </>
      )}
    </section>
  )
}

import { useEffect, useRef, useState } from 'react'
import type { Recipient } from '../messaging'
import { useChat } from './context'
import { useChatThreads } from './hooks'
import type { ChatThread } from './types'
import { ChatView } from './view/ChatView'
import { ThreadConversation } from './view/ThreadConversation'

export interface ChatPanelProps {
  /** Jump straight into the conversation with this agent (from an agent row or alert). */
  agent?: Recipient | null
  className?: string
}

/** The dock's chat: the shared ChatView in its compact, single-column form. Stateless apart from the open thread. */
export function ChatPanel({ agent, className }: ChatPanelProps) {
  const { client } = useChat()
  const { threads, error: threadsError, retry } = useChatThreads()
  const [openId, setOpenId] = useState<string | null>(null)
  const [openVersion, setOpenVersion] = useState(0)
  const [openError, setOpenError] = useState<string | null>(null)
  const [opened, setOpened] = useState<ChatThread | null>(null)
  const addr = agent?.address
  useEffect(() => {
    if (!agent) return
    let live = true
    client.openThread(agent).then(
      (t) => {
        if (live) {
          setOpenError(null)
          setOpened(t)
          setOpenId(t.id)
        }
      },
      (e) => { if (live) setOpenError(e instanceof Error ? e.message : 'Could not open conversation') },
    )
    return () => {
      live = false
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [client, addr, openVersion])

  const found = openId ? (threads?.find((t) => t.id === openId) ?? (opened?.id === openId ? opened : null)) : null
  const lastActive = useRef<ChatThread | null>(null)
  if (found) lastActive.current = found
  const active = found ?? (openId && lastActive.current?.id === openId ? lastActive.current : null)
  return (
    <section aria-label="Chat" className={`flex min-h-0 flex-col bg-bg ${className ?? ''}`}>
      <ChatView
        variant="compact"
        threads={threads}
        threadsError={threadsError || openError}
        onRetryThreads={() => { retry(); setOpenVersion((v) => v + 1) }}
        activeThread={active}
        onSelectThread={setOpenId}
        onBack={() => setOpenId(null)}
      >
        {active && <ThreadConversation key={active.id} thread={active} />}
      </ChatView>
    </section>
  )
}

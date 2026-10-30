import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import type { OverflowAction } from '@hollis-labs/design-components'
import { useChat } from '../context'
import { useChatThreads } from '../hooks'
import { chatRecipient, useChatRoster } from '../roster'
import type { ChatThread } from '../types'
import { ChatView } from './ChatView'
import { ThreadConversation } from './ThreadConversation'

/** /chat and /chat/:threadId. Wires the mail ChatClient into ChatView. */
export function ChatPage() {
  const { client } = useChat()
  const { threadId } = useParams()
  const navigate = useNavigate()
  const { threads, error, retry } = useChatThreads()
  const { agents } = useChatRoster()
  const [opened, setOpened] = useState<ChatThread | null>(null)
  const [openError, setOpenError] = useState<string | null>(null)

  const select = useCallback((id: string) => navigate(`/chat/${encodeURIComponent(id)}`), [navigate])
  const found = threadId ? (threads?.find((t) => t.id === threadId) ?? (opened?.id === threadId ? opened : null)) : null
  // A refresh of the list that briefly lacks the open thread must not unmount its conversation.
  const lastActive = useRef<ChatThread | null>(null)
  if (found) lastActive.current = found
  const active = found ?? (threadId && lastActive.current?.id === threadId ? lastActive.current : null)

  // A thread the list does not know yet (deep link, or just created) is looked up once it loads.
  useEffect(() => { if (opened && threads?.some((t) => t.id === opened.id)) setOpened(null) }, [threads, opened])

  // On a wide screen /chat opens the most recent conversation; below that the list stays the landing view.
  useEffect(() => {
    if (threadId || !threads?.length) return
    if (typeof window.matchMedia !== 'function' || !window.matchMedia('(min-width: 1024px)').matches) return
    navigate(`/chat/${encodeURIComponent(threads[0].id)}`, { replace: true })
  }, [threadId, threads, navigate])

  const newChatActions = useMemo<OverflowAction[]>(
    () => (agents ?? []).map((a) => ({
      label: a.display_name,
      onSelect: () => {
        setOpenError(null)
        client.openThread(chatRecipient(a)).then(
          (t) => { setOpened(t); select(t.id) },
          (e) => setOpenError(e instanceof Error ? e.message : 'Could not open conversation'),
        )
      },
    })),
    [agents, client, select],
  )

  return (
    <ChatView
      threads={threads}
      threadsError={error ?? openError}
      onRetryThreads={retry}
      activeThread={active}
      onSelectThread={select}
      newChatActions={newChatActions}
    >
      {active && <ThreadConversation key={active.id} thread={active} />}
    </ChatView>
  )
}

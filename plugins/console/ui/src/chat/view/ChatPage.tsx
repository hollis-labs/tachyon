import { useCallback, useEffect, useMemo, useState } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import type { OverflowAction } from '@hollis-labs/design-components'
import { useChat } from '../context'
import { useChatThread, useChatThreads } from '../hooks'
import { chatRecipient, useChatRoster } from '../roster'
import type { ChatThread } from '../types'
import { ChatView } from './ChatView'
import { ConversationView } from './ConversationView'

/** Keyed on the thread so hook state starts fresh per conversation. */
function ThreadConversation({ thread }: { thread: ChatThread }) {
  const c = useChatThread(thread.id)
  const cap = c.capability ?? thread.capability
  return (
    <ConversationView
      threadId={thread.id}
      messages={c.messages}
      agentLabel={thread.title}
      loading={c.loading}
      hasMore={c.hasMore}
      onLoadOlder={() => void c.loadOlder()}
      onSend={c.send}
      canSend={cap.canSend !== false}
      error={c.error}
      sendError={c.sendError}
    />
  )
}

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
  const active = threadId ? (threads?.find((t) => t.id === threadId) ?? (opened?.id === threadId ? opened : null)) : null

  // A thread the list does not know yet (deep link, or just created) is looked up once it loads.
  useEffect(() => { if (opened && threads?.some((t) => t.id === opened.id)) setOpened(null) }, [threads, opened])

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

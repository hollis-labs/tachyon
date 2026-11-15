import { createContext, useContext, useMemo, type ReactNode } from 'react'
import { defaultMessagingClient } from '../messaging/context'
import { OPERATOR_ADDRESS } from '../messaging/operator'
import { createMailChatClient } from './mail'
import type { ChatClient } from './types'

// Outside a provider: the live mail chat client (no roster), never a mock.
const defaultClient = createMailChatClient(defaultMessagingClient, { operator: OPERATOR_ADDRESS, agents: [] })
const ChatContext = createContext<{ client: ChatClient; operator?: string }>({ client: defaultClient })

/** Provides the ChatClient. The shell passes the mail client; a session-stream client can replace it here. */
export function ChatProvider({ client, operator, children }: { client: ChatClient; operator?: string; children: ReactNode }) {
  const value = useMemo(() => ({ client, operator }), [client, operator])
  return <ChatContext.Provider value={value}>{children}</ChatContext.Provider>
}

// eslint-disable-next-line react-refresh/only-export-components
export const useChat = () => useContext(ChatContext)

import { SlotProvider } from "@tachyon/host-sdk"
import { ApiProvider, useApi } from "@tachyon/torque-client"
import { type ReactNode, useState } from "react"
import { Route, Routes } from "react-router-dom"
import { LinkTextSlot, ShellAutolink } from "./autolink/LinkText"
import { withChatApiMessages } from "./chat/chatapi"
import { ChatProvider } from "./chat/context"
import { createMailChatClient } from "./chat/mail"
import { withChatApiSummaries } from "./chat/summaries"
import { createTetherChatClient } from "./chat/tether"
import { ChatPage } from "./chat/view/ChatPage"
import { consoleClient } from "./client"
import { createMailMessagingClient } from "./messaging/adapter"
import { MessagingProvider } from "./messaging/context"
import { OPERATOR_ADDRESS } from "./messaging/operator"
import { AgentUnreadProvider } from "./messaging/unread"

const consoleSlots = { LinkText: LinkTextSlot }

/**
 * The providers chat needs, from the old console layer (plugins/console/ui shell.tsx and
 * shell/ConsoleIntegration.tsx), in the same order. Not carried in G3: the alert host, the card runtime,
 * HITL and workflow clients, approvals, the operator prompt (no elevation), the chat dock and the app-agent
 * thread source (its client is not part of this build yet).
 */
function ConsoleChatProviders({ children }: { children: ReactNode }) {
  const api = useApi()
  const [{ messaging, chat }] = useState(() => {
    const messaging = createMailMessagingClient(api, { from: OPERATOR_ADDRESS })
    const chat = withChatApiMessages(
      withChatApiSummaries(
        createTetherChatClient(createMailChatClient(messaging, { operator: OPERATOR_ADDRESS, agents: [] })),
      ),
    )
    return { messaging, chat }
  })
  return (
    <MessagingProvider client={messaging} operator={OPERATOR_ADDRESS}>
      <AgentUnreadProvider>
        <ChatProvider client={chat} operator={OPERATOR_ADDRESS}>
          {children}
        </ChatProvider>
      </AgentUnreadProvider>
    </MessagingProvider>
  )
}

/** The chat surface (`/chat` in ui/app.json): the old console routes, resolved inside the page. */
export function ChatSurface() {
  return (
    <ApiProvider client={consoleClient}>
      <ShellAutolink>
        <SlotProvider slots={consoleSlots}>
          <ConsoleChatProviders>
            <Routes>
              <Route path="/chat" element={<ChatPage />} />
              <Route path="/chat/:threadId" element={<ChatPage />} />
            </Routes>
          </ConsoleChatProviders>
        </SlotProvider>
      </ShellAutolink>
    </ApiProvider>
  )
}

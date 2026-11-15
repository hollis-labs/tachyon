import { SlotProvider } from "@tachyon/host-sdk"
import { ApiProvider, useApi } from "@tachyon/torque-client"
import { type ReactNode, useState } from "react"
import { Route, Routes } from "react-router-dom"
import { ApprovalProvider } from "./approvals/context"
import { ApprovalsPage } from "./approvals/page"
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

/** The console base every console surface sits in, from the old shell.tsx: torque API, autolink, LinkText slot. */
function ConsoleBase({ children }: { children: ReactNode }) {
  return (
    <ApiProvider client={consoleClient}>
      <ShellAutolink>
        <SlotProvider slots={consoleSlots}>{children}</SlotProvider>
      </ShellAutolink>
    </ApiProvider>
  )
}

/** The chat surface (`/chat` in ui/app.json): the old console routes, resolved inside the page. */
export function ChatSurface() {
  return (
    <ConsoleBase>
      <ConsoleChatProviders>
        <Routes>
          <Route path="/chat" element={<ChatPage />} />
          <Route path="/chat/:threadId" element={<ChatPage />} />
        </Routes>
      </ConsoleChatProviders>
    </ConsoleBase>
  )
}

/**
 * The approvals surface (`/approvals` in ui/app.json): the old console route, with the ApprovalProvider the old
 * ConsoleIntegration placed above every console page. The page reads its selection from the query string.
 */
export function ApprovalsSurface() {
  return (
    <ConsoleBase>
      <ApprovalProvider>
        <Routes>
          <Route path="/approvals" element={<ApprovalsPage />} />
        </Routes>
      </ApprovalProvider>
    </ConsoleBase>
  )
}

import { SlotProvider } from "@tachyon/host-sdk"
import { ApiProvider, useApi } from "@tachyon/torque-client"
import { type ReactNode, useState } from "react"
import { Navigate, Route, Routes } from "react-router-dom"
import { AgentsPage } from "./agents-monitor/page"
import { ApprovalProvider } from "./approvals/context"
import { BuilderAgentsPage } from "./builder/BuilderAgentsPage"
import { BuilderLaunchesPage } from "./builder/BuilderLaunchesPage"
import { BuilderLibraryPage } from "./builder/BuilderLibraryPage"
import { BuilderTeamsPage } from "./builder/BuilderTeamsPage"
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
import { NeedsYouPage } from "./needs-you/page"
import { RegistryProjectsPage } from "./registry/RegistryProjectsPage"
import { TranscriptDashboard } from "./transcripts/page"

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

/** The Agent Ops surface (`/agents` in ui/app.json): the old console routes, the session id resolved inside the page. */
export function AgentsSurface() {
  return (
    <ConsoleBase>
      <Routes>
        <Route path="/agents" element={<AgentsPage />} />
        <Route path="/agents/:session" element={<AgentsPage />} />
      </Routes>
    </ConsoleBase>
  )
}

/** The Needs you surface (`/needs-you` in ui/app.json): the old console overlay route. */
export function NeedsYouSurface() {
  return (
    <ConsoleBase>
      <Routes>
        <Route path="/needs-you" element={<NeedsYouPage />} />
      </Routes>
    </ConsoleBase>
  )
}

/**
 * The Builder surface (`/builder` in ui/app.json): the old console routes. `/builder/classic` is not ported (plan
 * section 13); `/builder/projects` goes to the registry, as it did.
 */
export function BuilderSurface() {
  return (
    <ConsoleBase>
      <Routes>
        <Route path="/builder" element={<Navigate to="/builder/agents" replace />} />
        <Route path="/builder/agents" element={<BuilderAgentsPage />} />
        <Route path="/builder/agents/:id" element={<BuilderAgentsPage />} />
        <Route path="/builder/projects" element={<Navigate to="/registry/projects" replace />} />
        <Route path="/builder/teams" element={<BuilderTeamsPage />} />
        <Route path="/builder/launches" element={<BuilderLaunchesPage />} />
        <Route path="/builder/library" element={<BuilderLibraryPage />} />
        <Route path="/builder/library/:kind" element={<BuilderLibraryPage />} />
        <Route path="/builder/library/:kind/:id" element={<BuilderLibraryPage />} />
      </Routes>
    </ConsoleBase>
  )
}

/** The Registry surface (`/registry` in ui/app.json): the old console routes. */
export function RegistrySurface() {
  return (
    <ConsoleBase>
      <Routes>
        <Route path="/registry" element={<RegistryProjectsPage />} />
        <Route path="/registry/projects" element={<RegistryProjectsPage />} />
        <Route path="/registry/projects/:id" element={<RegistryProjectsPage />} />
      </Routes>
    </ConsoleBase>
  )
}

/** The Transcripts surface (`/dashboards/transcripts` in ui/app.json): the old console overlay route. */
export function TranscriptsSurface() {
  return (
    <ConsoleBase>
      <Routes>
        <Route path="/dashboards/transcripts" element={<TranscriptDashboard />} />
      </Routes>
    </ConsoleBase>
  )
}

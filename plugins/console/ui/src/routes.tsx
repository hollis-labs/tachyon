import { Navigate, useLocation } from "react-router-dom"
import { ApprovalsPage } from './approvals/page'
import { ChatPage } from './chat/view/ChatPage'
import type { PluginNavItem, PluginRoute } from "@tachyon/host-sdk"
import { GitPullRequest, MessageSquare, ShieldCheck, ChartNoAxesCombined } from "lucide-react"
import { TranscriptDashboard } from './transcripts/page'
import { PendingPrInbox, PR_INBOX_PATH } from "./forge/inbox"
import { PrWorkspacePage, PR_WORKSPACE_ROUTE } from "./forge/workspace"

/** Keep bookmarked inbox URLs, including query state, usable after the rename. */
function LegacyPrInboxRedirect() {
  const { search, hash } = useLocation()
  return <Navigate to={`${PR_INBOX_PATH}${search}${hash}`} replace />
}

export const overlayRoutes: PluginRoute[] = [
  { path: "/dashboards/transcripts", element: <TranscriptDashboard /> },
  { path: "/chat", element: <ChatPage /> },
  { path: "/chat/:threadId", element: <ChatPage /> },
  { path: "/approvals", element: <ApprovalsPage /> },
  { path: "/prs/inbox", element: <LegacyPrInboxRedirect /> },
  { path: PR_INBOX_PATH, element: <PendingPrInbox /> },
  { path: PR_WORKSPACE_ROUTE, element: <PrWorkspacePage /> },
]

export const overlayNav: PluginNavItem[] = [
  { key: "dashboards", label: "Dashboards", to: "/dashboards/transcripts", match: "/dashboards", icon: <ChartNoAxesCombined className="h-4 w-4" />, priority: 32 },
  { key: "chat", label: "Chat", to: "/chat", icon: <MessageSquare className="h-4 w-4" />, priority: 25 },
  { key: "approvals", label: "Approvals", to: "/approvals", icon: <ShieldCheck className="h-4 w-4" />, priority: 35 },
  {
    key: "pr-inbox",
    label: "Repos",
    to: PR_INBOX_PATH,
    icon: <GitPullRequest className="h-4 w-4" />,
    priority: 40,
  },
]

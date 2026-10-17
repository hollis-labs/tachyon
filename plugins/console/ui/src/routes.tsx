import { ApprovalsPage } from './approvals/page'
import type { PluginNavItem, PluginRoute } from "@tachyon/host-sdk"
import { GitPullRequest, ShieldCheck, ChartNoAxesCombined } from "lucide-react"
import { TranscriptDashboard } from './transcripts/page'
import { PendingPrInbox, PR_INBOX_PATH } from "./forge/inbox"
import { PrWorkspacePage, PR_WORKSPACE_ROUTE } from "./forge/workspace"

export const overlayRoutes: PluginRoute[] = [
  { path: "/dashboards/transcripts", element: <TranscriptDashboard /> },
  { path: "/approvals", element: <ApprovalsPage /> },
  { path: PR_INBOX_PATH, element: <PendingPrInbox /> },
  { path: PR_WORKSPACE_ROUTE, element: <PrWorkspacePage /> },
]

export const overlayNav: PluginNavItem[] = [
  { key: "dashboards", label: "Dashboards", to: "/dashboards/transcripts", match: "/dashboards", icon: <ChartNoAxesCombined className="h-4 w-4" />, priority: 32 },
  { key: "approvals", label: "Approvals", to: "/approvals", icon: <ShieldCheck className="h-4 w-4" />, priority: 35 },
  {
    key: "pr-inbox",
    label: "PR inbox",
    to: PR_INBOX_PATH,
    icon: <GitPullRequest className="h-4 w-4" />,
    priority: 40,
  },
]

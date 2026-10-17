import { ApprovalsPage } from './approvals/page'
import type { PluginNavItem, PluginRoute } from "@tachyon/host-sdk"
import { GitPullRequest, ShieldCheck } from "lucide-react"
import { PendingPrInbox, PR_INBOX_PATH } from "./forge/inbox"
import { PrWorkspacePage, PR_WORKSPACE_ROUTE } from "./forge/workspace"

export const overlayRoutes: PluginRoute[] = [
  { path: "/approvals", element: <ApprovalsPage /> },
  { path: PR_INBOX_PATH, element: <PendingPrInbox /> },
  { path: PR_WORKSPACE_ROUTE, element: <PrWorkspacePage /> },
]

export const overlayNav: PluginNavItem[] = [
  { key: "approvals", label: "Approvals", to: "/approvals", icon: <ShieldCheck className="h-4 w-4" />, priority: 35 },
  {
    key: "pr-inbox",
    label: "PR inbox",
    to: PR_INBOX_PATH,
    icon: <GitPullRequest className="h-4 w-4" />,
    priority: 40,
  },
]

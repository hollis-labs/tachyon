import type { PluginNavItem, PluginRoute } from "@tachyon/host-sdk"
import { GitPullRequest } from "lucide-react"
import { PendingPrInbox, PR_INBOX_PATH } from "./forge/inbox"
import { PrWorkspacePage, PR_WORKSPACE_ROUTE } from "./forge/workspace"

export const overlayRoutes: PluginRoute[] = [
  { path: PR_INBOX_PATH, element: <PendingPrInbox /> },
  { path: PR_WORKSPACE_ROUTE, element: <PrWorkspacePage /> },
]

export const overlayNav: PluginNavItem[] = [
  {
    key: "pr-inbox",
    label: "PR inbox",
    to: PR_INBOX_PATH,
    icon: <GitPullRequest className="h-4 w-4" />,
    priority: 40,
  },
]

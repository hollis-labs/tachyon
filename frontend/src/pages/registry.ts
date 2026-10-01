import type { ComponentType } from "react"
import { AgentOpsPage } from "./agent-ops"
import { DashboardPage } from "./dashboard"
import { SessionsPage } from "./sessions"

// Add a page here using the exact route declared in its plugin's nav metadata.
// The shell shows a plugin nav item only when its component and verb exist.
export const pageRegistry: Record<string, ComponentType> = {
  "/dashboard": DashboardPage,
  "/agents": AgentOpsPage,
  "/agents/durable": SessionsPage,
}

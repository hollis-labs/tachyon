import type { ComponentType } from "react"
import { AgentOpsPage } from "./agent-ops"
import { DashboardPage } from "./dashboard"
import { LaunchesPage } from "./launches"
import { SCMActivityPage, SCMRepositoriesPage } from "./scm"
import { SessionsPage } from "./sessions"
import { SettingsPage } from "./settings"
import { WorkPage } from "./work"
import { WorkBoardPage } from "./work-board"

// Add a page here using the exact route declared in its plugin's nav metadata.
// The shell shows a plugin nav item only when its component and verb exist.
export const pageRegistry: Record<string, ComponentType> = {
  "/dashboard": DashboardPage,
  "/settings": SettingsPage,
  "/launches": LaunchesPage,
  "/agents": AgentOpsPage,
  "/agents/durable": SessionsPage,
  "/work": WorkPage,
  "/work/board": WorkBoardPage,
  "/scm": SCMRepositoriesPage,
  "/scm/activity": SCMActivityPage,
}

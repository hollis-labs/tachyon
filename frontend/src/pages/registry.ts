import type { ComponentType } from "react"
import { AgentOpsPage } from "./agent-ops"
import { DashboardPage } from "./dashboard"
import { LaunchesPage } from "./launches"
import { ObservePage } from "./observe"
import { ObserveLogsPage } from "./observe-logs"
import { ObserveMetricsPage } from "./observe-metrics"
import { ProviderSessionsPage } from "./provider-sessions"
import { SCMActivityPage, SCMRepositoriesPage } from "./scm"
import { ServiceHealthPage } from "./service-health"
import { ServicesPage } from "./services"
import { SessionHistoryPage } from "./session-history"
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
  "/sessions": ProviderSessionsPage,
  "/sessions/history": SessionHistoryPage,
  "/work": WorkPage,
  "/work/board": WorkBoardPage,
  "/services": ServicesPage,
  "/services/health": ServiceHealthPage,
  "/observe": ObservePage,
  "/observe/logs": ObserveLogsPage,
  "/observe/metrics": ObserveMetricsPage,
  "/scm": SCMRepositoriesPage,
  "/scm/activity": SCMActivityPage,
}

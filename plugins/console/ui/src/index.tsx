import type { TachyonPlugin } from "@tachyon/host-sdk"
import { Bot, Hammer, Library } from "lucide-react"
import { Navigate } from "react-router-dom"
import { BuilderPage } from "./agents"
import { ApiProvider } from "@tachyon/torque-client"
import { consoleClient } from "@console/client"
import { AgentsPage } from "./agents-monitor/page"
import { BuilderAgentsPage } from "./builder/BuilderAgentsPage"
import { BuilderTeamsPage } from "./builder/BuilderTeamsPage"
import { BuilderLaunchesPage } from "./builder/BuilderLaunchesPage"
import { BuilderLibraryPage } from "./builder/BuilderLibraryPage"
import { RegistryProjectsPage } from "./registry/RegistryProjectsPage"
import { overlayNav, overlayRoutes } from "./routes"
import { ConsoleLayer, ConsoleNavExtras } from "./shell"

export { AlertsSettingsSection } from "./alerts/AlertsSettingsSection"
export { SessionBanner } from "./operator/SessionBanner"

export const consolePlugin: TachyonPlugin = {
  id: "console",
  name: "Console",
  nav: [
    { key: "agents", label: "Agents", to: "/agents", icon: <Bot className="h-4 w-4" />, priority: 20 },
    { key: "builder", label: "Builder", to: "/builder", icon: <Hammer className="h-4 w-4" />, priority: 30 },
    { key: "registry", label: "Registry", to: "/registry/projects", icon: <Library className="h-4 w-4" />, priority: 35 },
    ...overlayNav,
  ],
  routes: [
    { path: "/agents", element: <ApiProvider client={consoleClient}><AgentsPage /></ApiProvider> },
    { path: "/agents/:session", element: <ApiProvider client={consoleClient}><AgentsPage /></ApiProvider> },
    // /builder is the new Builder; the older tabbed page stays reachable for Teams, Assignments, Projects and Workstreams.
    { path: "/builder", element: <Navigate to="/builder/agents" replace /> },
    { path: "/builder/classic", element: <BuilderPage /> },
    { path: "/builder/agents", element: <BuilderAgentsPage /> },
    { path: "/builder/agents/:id", element: <BuilderAgentsPage /> },
    { path: "/builder/projects", element: <Navigate to="/registry/projects" replace /> },
    { path: "/builder/teams", element: <BuilderTeamsPage /> },
    { path: "/builder/launches", element: <BuilderLaunchesPage /> },
    { path: "/builder/library", element: <BuilderLibraryPage /> },
    { path: "/builder/library/:kind", element: <BuilderLibraryPage /> },
    { path: "/builder/library/:kind/:id", element: <BuilderLibraryPage /> },
    { path: "/registry", element: <RegistryProjectsPage /> },
    { path: "/registry/projects", element: <RegistryProjectsPage /> },
    { path: "/registry/projects/:id", element: <RegistryProjectsPage /> },
    ...overlayRoutes,
  ],
  Shell: ConsoleLayer,
  NavExtras: ConsoleNavExtras,
}

import type { TachyonPlugin } from "@tachyon/host-sdk"
import { Bot, Hammer } from "lucide-react"
import { BuilderPage } from "./agents"
import { ApiProvider } from "@tachyon/torque-client"
import { consoleClient } from "@console/client"
import { AgentsPage } from "./agents-monitor/page"
import { overlayNav, overlayRoutes } from "./routes"
import { ConsoleLayer, ConsoleNavExtras } from "./shell"

export const consolePlugin: TachyonPlugin = {
  id: "console",
  name: "Console",
  nav: [
    { key: "agents", label: "Agents", to: "/agents", icon: <Bot className="h-4 w-4" />, priority: 20 },
    { key: "builder", label: "Builder", to: "/builder", icon: <Hammer className="h-4 w-4" />, priority: 30 },
    ...overlayNav,
  ],
  routes: [
    { path: "/agents", element: <ApiProvider client={consoleClient}><AgentsPage /></ApiProvider> },
    { path: "/agents/:session", element: <ApiProvider client={consoleClient}><AgentsPage /></ApiProvider> },
    { path: "/builder", element: <BuilderPage /> },
    ...overlayRoutes,
  ],
  Shell: ConsoleLayer,
  NavExtras: ConsoleNavExtras,
}

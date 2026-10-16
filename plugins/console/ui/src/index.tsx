import type { TachyonPlugin } from "@tachyon/host-sdk"
import { Bot, Hammer } from "lucide-react"
import { BuilderPage } from "./agents"
import { FleetAgentsPage } from "./fleet"
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
    { path: "/agents", element: <FleetAgentsPage /> },
    { path: "/builder", element: <BuilderPage /> },
    ...overlayRoutes,
  ],
  Shell: ConsoleLayer,
  NavExtras: ConsoleNavExtras,
}

import { NavRail, type NavRailItem, ThemeSwitcher } from "@hollis-labs/sysop-ui"
import { Activity, LayoutDashboard, Radio, Users } from "lucide-react"
import { useState } from "react"
import { AgentOpsPage } from "./pages/agent-ops"
import { DashboardPage } from "./pages/dashboard"
import { SessionsPage } from "./pages/sessions"

/**
 * App shell — the icon nav rail on the left, a pinned page header, and the
 * active page. Add pages by extending `nav` and the `route` switch below.
 */
export function App() {
  // Agent Ops is the only real content this MVP has; land there directly
  // instead of flashing the starter Dashboard page first.
  const [route, setRoute] = useState("agent-ops")

  const nav: NavRailItem[] = [
    {
      key: "dashboard",
      label: "Dashboard",
      icon: <LayoutDashboard className="h-4 w-4" />,
      active: route === "dashboard",
      onSelect: () => setRoute("dashboard"),
    },
    {
      key: "agent-ops",
      label: "Agent Ops",
      icon: <Users className="h-4 w-4" />,
      active: route === "agent-ops",
      onSelect: () => setRoute("agent-ops"),
    },
    {
      key: "sessions",
      label: "Sessions",
      icon: <Radio className="h-4 w-4" />,
      active: route === "sessions",
      onSelect: () => setRoute("sessions"),
    },
  ]

  return (
    <div className="flex h-screen bg-bg text-text">
      <NavRail
        items={nav}
        logo={<Activity className="h-4 w-4" />}
        logoLabel="Sysop"
        footerExtra={<ThemeSwitcher />}
      />
      <div className="flex min-w-0 flex-1 flex-col">
        <main className="min-h-0 flex-1 overflow-auto">
          {route === "dashboard" && <DashboardPage />}
          {route === "agent-ops" && <AgentOpsPage />}
          {route === "sessions" && <SessionsPage />}
        </main>
      </div>
    </div>
  )
}

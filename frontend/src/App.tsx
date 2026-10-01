import { NavRail, type NavRailItem, ThemeSwitcher } from "@hollis-labs/sysop-ui"
import {
  Activity,
  GitBranch,
  LayoutDashboard,
  Radio,
  Rocket,
  Server,
  Settings,
  Terminal,
  Users,
} from "lucide-react"
import { useEffect, useMemo, useState } from "react"
import { fetchNavigation, type Navigation, visibleNavigation } from "./api/navigation"
import { useCapabilities } from "./hooks/use-capabilities"
import { pageRegistry } from "./pages/registry"

const icons = {
  activity: Activity,
  git: GitBranch,
  play: Rocket,
  server: Server,
  settings: Settings,
  terminal: Terminal,
  users: Users,
  radio: Radio,
}

export function App() {
  const capabilities = useCapabilities()
  const [route, setRoute] = useState("/agents")
  const [navigation, setNavigation] = useState<Navigation>({})
  const [loading, setLoading] = useState(true)
  const [navError, setNavError] = useState("")
  useEffect(() => {
    let active = true
    fetchNavigation()
      .then((nav) => {
        if (active) {
          setNavigation(nav)
          setNavError("")
        }
      })
      .catch(() => {
        if (active) setNavError("Navigation is unavailable. Refresh to try again.")
      })
      .finally(() => {
        if (active) setLoading(false)
      })
    return () => {
      active = false
    }
  }, [])

  const items = useMemo(() => {
    // An older host has neither endpoint: retain the original agent surfaces
    // only when legacy provider discovery has succeeded.
    const nav =
      capabilities.available === false
        ? {
            groups: [{ id: "agents", label: "Agents", icon: "users", priority: 100 }],
            items: [
              {
                id: "agents",
                label: "Agent Ops",
                group: "agents",
                route: "/agents",
                requires_verb: "agent_list",
              },
              {
                id: "durable",
                label: "Durable Sessions",
                group: "agents",
                route: "/agents/durable",
                requires_verb: "agent_list",
              },
            ],
          }
        : navigation
    return visibleNavigation(nav, capabilities.has, (path) => Object.hasOwn(pageRegistry, path))
  }, [navigation, capabilities])

  const activeRoute =
    items.some((item) => item.route === route) || route === "/dashboard" ? route : "/dashboard"
  const Page = pageRegistry[activeRoute]
  const nav: NavRailItem[] = [
    {
      key: "dashboard",
      label: "Dashboard",
      icon: <LayoutDashboard className="h-4 w-4" />,
      active: activeRoute === "/dashboard",
      onSelect: () => setRoute("/dashboard"),
    },
    ...items.map((item) => {
      const iconName = navigation.groups?.find((group) => group.id === item.group)?.icon
      const Icon = icons[iconName as keyof typeof icons] ?? Activity
      return {
        key: item.id,
        label: item.label,
        icon: <Icon className="h-4 w-4" />,
        active: activeRoute === item.route,
        onSelect: () => setRoute(item.route),
      }
    }),
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
          {loading || capabilities.loading ? (
            <p className="p-4" role="status">
              Loading capabilities…
            </p>
          ) : (
            <>
              {navError && capabilities.available !== false && (
                <p className="p-4 text-text-muted" role="status">
                  {navError}
                </p>
              )}
              <Page />
            </>
          )}
        </main>
      </div>
    </div>
  )
}

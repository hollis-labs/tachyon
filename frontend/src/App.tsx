import { AppShell, Toaster } from "@hollis-labs/design-components"
import { NavRail, type NavRailItem, ThemeSwitcher } from "@hollis-labs/kit-dashboard"
import { Activity, LayoutDashboard } from "lucide-react"
import { useEffect, useMemo, useState } from "react"
import { fetchNavigation, type Navigation, visibleNavigation } from "./api/navigation"
import { type ConfigTarget, retiredConfigTargets } from "./api/settings"
import { useCapabilities } from "./hooks/use-capabilities"
import { getNavIcon } from "./icons"
import { NotAvailablePage } from "./pages/not-available"
import { PluginRecoveryPage } from "./pages/plugin-recovery"
import { pageRegistry } from "./pages/registry"
import { compiledOwner } from "./routing/compiled-owners"
import { PageRouteProvider, useHashRoute } from "./routing/hash-route"
import { legacyRouteCatalog, parseLocation, resolveRoute } from "./routing/routes"

export function App() {
  const capabilities = useCapabilities()
  const location = useHashRoute()
  const setRoute = location.navigate
  const [navigation, setNavigation] = useState<Navigation>({})
  const [loading, setLoading] = useState(true)
  const [navError, setNavError] = useState("")
  const [retired, setRetired] = useState<ConfigTarget[]>([])
  const [registryError, setRegistryError] = useState("")
  const [registryLoading, setRegistryLoading] = useState(true)
  const [restoreFocus, setRestoreFocus] = useState(false)
  useEffect(() => {
    let active = true
    retiredConfigTargets()
      .then((targets) => {
        if (active) setRetired(targets)
      })
      .catch(() => {
        if (active)
          setRegistryError("Plugin recovery metadata is unavailable. Refresh to try again.")
      })
      .finally(() => {
        if (active) setRegistryLoading(false)
      })
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

  const routeNavigation = useMemo(() => {
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
    return nav
  }, [navigation, capabilities.available])
  const catalog = useMemo(() => {
    const catalog = legacyRouteCatalog(routeNavigation)
    catalog.pages.push({
      id: "host-dashboard",
      route: "/dashboard",
      title: "Dashboard",
      view: "legacy-route:/dashboard",
    })
    return catalog
  }, [routeNavigation])
  const items = useMemo(
    () =>
      visibleNavigation(
        {
          ...routeNavigation,
          items: routeNavigation.items?.filter((item) =>
            catalog.items.some((admitted) => admitted.id === item.id),
          ),
        },
        capabilities.has,
        (path) => Object.hasOwn(pageRegistry, path),
      ),
    [routeNavigation, catalog, capabilities],
  )
  const resolution = resolveRoute(location, catalog, {
    hasVerb: capabilities.has,
    hasView: (view) =>
      view.startsWith("legacy-route:") &&
      Object.hasOwn(pageRegistry, view.slice("legacy-route:".length)),
    retired,
    compiledOwner,
    discoveryError:
      capabilities.available === false
        ? undefined
        : navError || capabilities.refreshError || undefined,
  })

  // Recovery is a host surface, never a plugin capability or nav declaration.
  const recoveryAvailable =
    !loading && retired.length > 0 && !navigation.items?.some((item) => item.route === "/settings")
  async function refreshRecovery() {
    const [nextNavigation, nextRetired] = await Promise.all([
      fetchNavigation(),
      retiredConfigTargets(),
      capabilities.refresh(),
    ])
    setNavigation(nextNavigation)
    setRetired(nextRetired)
    setNavError("")
    setRegistryError("")
    const stillOnRecovery = parseLocation(window.location.hash).path === "/plugin-recovery"
    if (
      stillOnRecovery &&
      (nextRetired.length === 0 || nextNavigation.items?.some((item) => item.route === "/settings"))
    ) {
      setRoute(
        nextNavigation.items?.some((item) => item.route === "/settings")
          ? "/settings"
          : "/dashboard",
        true,
      )
    }
    setRestoreFocus(stillOnRecovery)
  }
  useEffect(() => {
    if (!restoreFocus) return
    const label = items.some((item) => item.route === "/settings")
      ? items.find((item) => item.route === "/settings")?.label
      : recoveryAvailable
        ? "Plugin recovery"
        : "Dashboard"
    Array.from(document.querySelectorAll<HTMLButtonElement>("button[aria-label]"))
      .find((button) => button.getAttribute("aria-label") === label)
      ?.focus()
    setRestoreFocus(false)
  }, [restoreFocus, items, recoveryAvailable])
  const activeRoute = location.path
  const Page = resolution.page
    ? pageRegistry[resolution.page.view.slice("legacy-route:".length)]
    : undefined
  const nav: NavRailItem[] = [
    {
      key: "dashboard",
      label: "Dashboard",
      icon: <LayoutDashboard className="h-4 w-4" />,
      active: activeRoute === "/dashboard",
      onSelect: () => setRoute("/dashboard"),
    },
    ...(recoveryAvailable
      ? [
          {
            key: "host-plugin-recovery",
            label: "Plugin recovery",
            icon: <Activity className="h-4 w-4" />,
            active: activeRoute === "/plugin-recovery",
            onSelect: () => setRoute("/plugin-recovery"),
          },
        ]
      : []),
    ...items.map((item) => {
      const iconName = navigation.groups?.find((group) => group.id === item.group)?.icon
      const Icon = getNavIcon(iconName)
      return {
        key: item.id,
        label: item.label,
        icon: <Icon className="h-4 w-4" />,
        active: resolution.itemId === item.id && !resolution.reason,
        onSelect: () => setRoute(item.route),
      }
    }),
  ]

  return (
    <AppShell
      className="text-text"
      nav={
        <NavRail
          items={nav}
          logo={<Activity className="h-4 w-4" />}
          logoLabel="Sysop"
          footerExtra={<ThemeSwitcher />}
        />
      }
    >
      <div className="flex min-h-0 min-w-0 flex-1 flex-col overflow-auto">
        {loading || capabilities.loading || registryLoading ? (
          <p className="p-4" role="status">
            Loading capabilities…
          </p>
        ) : (
          <>
            {(navError || capabilities.refreshError) && capabilities.available !== false && (
              <p className="p-4 text-text-muted" role="status">
                {navError || capabilities.refreshError}
              </p>
            )}
            {registryError && (
              <p className="p-4 text-text-muted" role="status">
                {registryError}
              </p>
            )}
            {resolution.breadcrumbs.length > 0 && activeRoute !== "/plugin-recovery" && (
              <nav aria-label="Breadcrumbs" className="border-b border-border px-4 py-2 text-sm">
                <ol className="flex flex-wrap items-center gap-2">
                  {resolution.breadcrumbs.map((crumb, index) => (
                    <li key={crumb.id} className="flex items-center gap-2">
                      {index > 0 && <span aria-hidden="true">›</span>}
                      {crumb.route && index < resolution.breadcrumbs.length - 1 ? (
                        <a className="underline" href={`#${crumb.route}`}>
                          {crumb.label}
                        </a>
                      ) : (
                        <span
                          aria-current={
                            index === resolution.breadcrumbs.length - 1 ? "page" : undefined
                          }
                        >
                          {crumb.label}
                        </span>
                      )}
                    </li>
                  ))}
                </ol>
              </nav>
            )}
            {activeRoute === "/plugin-recovery" && location.valid && recoveryAvailable ? (
              <PluginRecoveryPage targets={retired} onRefresh={refreshRecovery} />
            ) : activeRoute === "/plugin-recovery" && location.valid ? (
              <NotAvailablePage
                path={location.path}
                reason="recovery-unavailable"
                detail={
                  registryError ||
                  (items.some((item) => item.route === "/settings")
                    ? "Plugin recovery is available in Settings."
                    : "The host reports no retired plugins requiring recovery.")
                }
              />
            ) : resolution.reason || !Page ? (
              <NotAvailablePage
                path={location.path}
                reason={resolution.reason}
                detail={resolution.detail || "No host-compiled page is registered for this route."}
              />
            ) : (
              <PageRouteProvider route={{ ...location, params: resolution.params }}>
                <Page />
              </PageRouteProvider>
            )}
          </>
        )}
      </div>
      <Toaster />
    </AppShell>
  )
}

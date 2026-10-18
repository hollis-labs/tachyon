import { EmptyState, Toaster, TooltipProvider } from "@hollis-labs/design-components"
import { NavRail, type NavRailItem, ThemeSwitcher, useDashboardDocumentScale } from "@hollis-labs/kit-dashboard"
import {
  type HostPluginInfo,
  HostPluginsProvider,
  type PluginNavItem,
  type TachyonPlugin,
  useHostPlugins,
} from "@tachyon/host-sdk"
import { Atom } from "lucide-react"
import { type ReactNode, useEffect, useState } from "react"
import { BrowserRouter, Navigate, Route, Routes, useLocation, useNavigate } from "react-router-dom"
import { useApi } from "./api/context"
import { DataStatus } from "./DataStatus"
import { plugins } from "./plugins"

const DEFAULT_ROUTE = "/planner"

/** Gate a plugin's pages on its backend being admitted by the host. */
function PluginGate({ plugin, children }: { plugin: TachyonPlugin; children: ReactNode }) {
  const inventory = useHostPlugins()
  if (inventory === null) return null
  const info = inventory.find((p) => p.id === plugin.id)
  if (info?.status === "running") return <>{children}</>
  return (
    <div className="p-6">
      <EmptyState
        variant="error"
        title={`${plugin.name} is unavailable`}
        description={info?.reason ?? `The ${plugin.name} plugin backend is not running.`}
      />
    </div>
  )
}

function Shell() {
  const navigate = useNavigate()
  const location = useLocation()
  // The shell owns geometry for every route, including portalled page controls.
  useDashboardDocumentScale("reference")

  const nav: (PluginNavItem & { plugin: string })[] = plugins
    .flatMap((p) => (p.nav ?? []).map((n) => ({ ...n, plugin: p.id })))
    .sort((a, b) => a.priority - b.priority)

  const items: NavRailItem[] = nav.map((n) => ({
    key: `${n.plugin}:${n.key}`,
    label: n.label,
    icon: n.icon,
    footer: n.footer,
    active: location.pathname.startsWith(n.match ?? n.to),
    onSelect: () => navigate(n.to),
  }))

  const extras = plugins.flatMap((p) => (p.NavExtras ? [<p.NavExtras key={p.id} />] : []))

  return (
    <div className="tachyon-shell flex h-full w-full overflow-hidden bg-bg text-foreground">
      <NavRail
        items={items}
        logo={<Atom className="h-5 w-5" />}
        logoLabel="Tachyon"
        footerExtra={
          <>
            {extras}
            <ThemeSwitcher />
          </>
        }
      />
      <main className="flex min-w-0 flex-1 flex-col overflow-hidden bg-bg">
        <DataStatus />
        <div className="min-h-0 flex-1 overflow-auto">
          <Routes>
            <Route path="/" element={<Navigate to={DEFAULT_ROUTE} replace />} />
            {plugins.flatMap((p) =>
              p.routes.map((r) => (
                <Route
                  key={`${p.id}:${r.path}`}
                  path={r.path}
                  element={<PluginGate plugin={p}>{r.element}</PluginGate>}
                />
              )),
            )}
            <Route path="*" element={<Navigate to={DEFAULT_ROUTE} replace />} />
          </Routes>
        </div>
      </main>
    </div>
  )
}

/** Nest plugin shell layers (providers/overlays) around the routed shell. */
function withShellLayers(children: ReactNode): ReactNode {
  return plugins.reduceRight<ReactNode>(
    (inner, p) => (p.Shell ? <p.Shell key={p.id}>{inner}</p.Shell> : inner),
    children,
  )
}

export function App() {
  const api = useApi()
  const [inventory, setInventory] = useState<HostPluginInfo[] | null>(null)

  useEffect(() => {
    let live = true
    api
      .getPlugins()
      .then((r) => live && setInventory(r.plugins))
      .catch(() => live && setInventory([]))
    return () => {
      live = false
    }
  }, [api])

  return (
    <HostPluginsProvider value={inventory}>
      <TooltipProvider>
        <BrowserRouter>{withShellLayers(<Shell />)}</BrowserRouter>
        <Toaster position="bottom-right" />
      </TooltipProvider>
    </HostPluginsProvider>
  )
}

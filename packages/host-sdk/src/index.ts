/**
 * @tachyon/host-sdk — the contract between the Tachyon host shell and its
 * plugin UIs.
 *
 * Phase 1: plugin UIs are workspace packages compiled into the host frontend.
 * The contract is kept narrow (routes, nav, shell layers, slots, API/stream
 * URLs) so the same modules can later load as runtime bundles
 * (CW-20261016-0165) without changing plugin code.
 */
import { type ComponentType, createContext, createElement, type ReactNode, useContext, useMemo } from "react"

export interface PluginRoute {
  /** react-router path, absolute (e.g. "/planner/:db"). */
  path: string
  element: ReactNode
}

export interface PluginNavItem {
  key: string
  label: string
  /** Route the nav item opens. Active when location starts with `match ?? to`. */
  to: string
  match?: string
  icon: ReactNode
  /** Lower sorts first. Host-owned destinations use 0 and 1000. */
  priority: number
  footer?: boolean
}

export interface TachyonPlugin {
  /** Must equal the backend plugin id in the host catalog. */
  id: string
  name: string
  routes: PluginRoute[]
  nav?: PluginNavItem[]
  /**
   * Optional layer wrapping the whole routed app (providers, overlays,
   * toasts). Layers nest in plugin registration order, inside the router.
   */
  Shell?: ComponentType<{ children: ReactNode }>
  /** Optional element rendered in the nav rail footer. */
  NavExtras?: ComponentType
}

/** Same-origin base for a plugin's buffered API routes. */
export function pluginApiBase(id: string): string {
  return `/api/plugins/${encodeURIComponent(id)}`
}

/** Same-origin URL for a host-relayed SSE stream declared by a plugin. */
export function pluginStreamUrl(id: string, name: string, query?: Record<string, string | undefined>): string {
  const base = `${pluginApiBase(id)}/stream/${encodeURIComponent(name)}`
  if (!query) return base
  const params = new URLSearchParams()
  for (const [k, v] of Object.entries(query)) if (v !== undefined && v !== "") params.set(k, v)
  const qs = params.toString()
  return qs ? `${base}?${qs}` : base
}

// ---------------------------------------------------------------------------
// Slots: typed, optional capabilities one plugin provides to others. A
// consumer must render sensibly when a slot is absent (plugin not installed).
// ---------------------------------------------------------------------------

export interface LinkTextSlotProps {
  /** Autolink surface name (lets the provider honour per-surface opt-outs). */
  surface: string
  text: string
  includeCode?: boolean
  kinds?: string[]
}

export interface HostSlots {
  LinkText?: ComponentType<LinkTextSlotProps>
}

const SlotsContext = createContext<HostSlots>({})

/** Provide (and merge) slots for descendants. Inner providers win. */
export function SlotProvider({ slots, children }: { slots: HostSlots; children: ReactNode }) {
  const outer = useContext(SlotsContext)
  const merged = useMemo(() => ({ ...outer, ...slots }), [outer, slots])
  return createElement(SlotsContext.Provider, { value: merged }, children)
}

export function useSlot<K extends keyof HostSlots>(name: K): HostSlots[K] {
  return useContext(SlotsContext)[name]
}

/** Linkified text when a LinkText provider is installed, plain text otherwise. */
export function HostLinkText(props: LinkTextSlotProps) {
  const Impl = useSlot("LinkText")
  if (!Impl) return createElement("span", null, props.text)
  return createElement(Impl, props)
}

// ---------------------------------------------------------------------------
// Host plugin status (from GET /api/plugins).
// ---------------------------------------------------------------------------

export type PluginStatus = "running" | "stopped" | "unavailable"

export interface HostPluginInfo {
  id: string
  name: string
  version: string
  description: string
  status: PluginStatus
  reason?: string
  streams: string[]
}

const HostPluginsContext = createContext<HostPluginInfo[] | null>(null)

export const HostPluginsProvider = HostPluginsContext.Provider

/** Host-reported plugin inventory; null while loading. */
export function useHostPlugins(): HostPluginInfo[] | null {
  return useContext(HostPluginsContext)
}

/** True when the plugin backend is admitted and running. */
export function usePluginAvailable(id: string): boolean {
  const plugins = useHostPlugins()
  return plugins?.some((p) => p.id === id && p.status === "running") ?? false
}

export { hostFetch, responseFreshness, useDataFreshness, dataRefreshRegistry, refreshPageData } from "./freshness"
export type { DataFreshness } from "./freshness"

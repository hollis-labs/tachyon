import type { Navigation } from "../api/navigation"

// Adapter seam for 0118: admitted pages are independent of visible nav items.
// This is a host model, not another plugin delivery or wire contract.
export interface RoutePage {
  id: string
  route: string
  title: string
  view: string
  owner?: string
  hidden?: boolean
  requiresVerbs?: string[]
}
export interface RouteItem {
  id: string
  label: string
  group: string
  page: string
  hidden?: boolean
  requiresVerbs?: string[]
}
export interface RouteSubview {
  id: string
  label: string
  parent: string
  page: string
  requiresVerbs?: string[]
}
export interface RouteCatalog {
  pages: RoutePage[]
  items: RouteItem[]
  groups: { id: string; label: string }[]
  subviews: RouteSubview[]
}
export interface RouteLocation {
  path: string
  query: URLSearchParams
  valid: boolean
}

function hasControl(value: string) {
  return Array.from(value).some((character) => {
    const code = character.charCodeAt(0)
    return code < 32 || code === 127
  })
}

export function parseLocation(hash: string): RouteLocation {
  const value = hash.replace(/^#/, "") || "/agents"
  const split = value.indexOf("?")
  const path = split < 0 ? value : value.slice(0, split)
  const query = new URLSearchParams(split < 0 ? "" : value.slice(split + 1))
  let valid = path.startsWith("/") && path.length <= 2048 && !/[#\\\s]/.test(path)
  const segments = path.slice(1).split("/")
  try {
    valid &&= segments.every((segment) => {
      const decoded = decodeURIComponent(segment)
      return !!decoded && decoded !== "." && decoded !== ".." && !hasControl(decoded)
    })
  } catch {
    valid = false
  }
  return { path, query, valid }
}

// Canonical ADR002 R3: static declaration identity. Named-pattern admission is
// held pending owner disposition; the bounded proposal is retained in proof
// scratch only. Query strings belong to the page, never to route identity.
export function validPattern(pattern: string): boolean {
  return (
    pattern.length <= 128 &&
    /^\/[a-z0-9][a-z0-9/_-]*$/.test(pattern) &&
    !pattern.endsWith("/") &&
    !pattern.includes("//")
  )
}

export function matchRoute(pattern: string, path: string): Record<string, string> | null {
  if (!validPattern(pattern) || !parseLocation(path).valid || path.includes("?")) return null
  return pattern === path ? {} : null
}

export function routePath(pattern: string): string {
  if (!validPattern(pattern)) throw new Error("Invalid route pattern")
  return pattern
}

export function legacyRouteCatalog(nav: Navigation): RouteCatalog {
  const items = (nav.items ?? []).filter((item) => {
    if (!validPattern(item.route)) return false
    if (/^\/(dashboard|plugin-recovery)(\/|$)/.test(item.route)) return false
    return !/^\/settings(\/|$)/.test(item.route) || item.plugin_id === "config-ops"
  })
  return {
    groups: nav.groups ?? [],
    items: items.map((item) => ({ ...item, page: item.id })),
    pages: items.map((item) => ({
      id: item.id,
      route: item.route,
      title: item.label,
      view: `legacy-route:${item.route}`,
      owner: item.plugin_id,
      requiresVerbs: item.requires_verb ? [item.requires_verb] : [],
    })),
    subviews: [],
  }
}

export interface Breadcrumb {
  id: string
  label: string
  route?: string
}
export type RouteResolution = {
  page?: RoutePage
  params: Record<string, string>
  itemId?: string
  breadcrumbs: Breadcrumb[]
  reason?:
    | "invalid-route"
    | "unknown-route"
    | "plugin-retired"
    | "missing-verb"
    | "unregistered-page"
    | "discovery-unavailable"
  detail?: string
}

export function resolveRoute(
  location: RouteLocation,
  catalog: RouteCatalog,
  inputs: {
    hasVerb(verb: string): boolean
    hasView(view: string): boolean
    retired: { id: string; reason?: string }[]
    compiledOwner(path: string): string | undefined
    discoveryError?: string
  },
): RouteResolution {
  const unavailable = (reason: RouteResolution["reason"], detail: string): RouteResolution => ({
    params: {},
    breadcrumbs: [],
    reason,
    detail,
  })
  if (!location.valid)
    return unavailable("invalid-route", "This address has invalid path encoding or route syntax.")
  // Current declarations use exact/static paths. A prefix is never a subview.
  const matches = catalog.pages
    .map((page) => ({ page, params: matchRoute(page.route, location.path) }))
    .filter((match) => match.params !== null)
  const match = matches[0]
  const owner = match?.page.owner ?? inputs.compiledOwner(location.path)
  const retired = owner && inputs.retired.find((plugin) => plugin.id === owner)
  if (retired)
    return unavailable(
      "plugin-retired",
      `The host reports plugin ${retired.id} as retired.${retired.reason ? ` Reason: ${retired.reason}` : ""}`,
    )
  if (!match) {
    if (inputs.discoveryError) return unavailable("discovery-unavailable", inputs.discoveryError)
    return unavailable(
      "unknown-route",
      "No available declaration registers this route. An unknown or hidden undeclared route cannot be opened.",
    )
  }
  const { page, params } = match
  const subview = catalog.subviews.find((view) => view.page === page.id)
  const item = subview
    ? catalog.items.find((item) => item.id === subview.parent)
    : catalog.items.find((item) => item.page === page.id)
  const group = catalog.groups.find((group) => group.id === item?.group)
  const parentPage = catalog.pages.find((page) => page.id === item?.page)
  const parentRoute =
    parentPage &&
    (() => {
      try {
        return routePath(parentPage.route)
      } catch {
        return undefined
      }
    })()
  const breadcrumbs: Breadcrumb[] = [
    ...(group ? [{ id: `group:${group.id}`, label: group.label }] : []),
    ...(item ? [{ id: `item:${item.id}`, label: item.label, route: parentRoute }] : []),
    ...(subview
      ? [{ id: `subview:${subview.id}`, label: subview.label }]
      : !item
        ? [{ id: `page:${page.id}`, label: page.title }]
        : []),
  ]
  const result = { page, params: params ?? {}, itemId: item?.id, breadcrumbs }
  const missing = Array.from(
    new Set([
      ...(page.requiresVerbs ?? []),
      ...(item?.requiresVerbs ?? []),
      ...(subview?.requiresVerbs ?? []),
    ]),
  ).filter((verb) => !inputs.hasVerb(verb))
  if (missing.length)
    return {
      ...result,
      reason: "missing-verb",
      detail: `Required verbs are unavailable: ${missing.join(", ")}.`,
    }
  if (!inputs.hasView(page.view))
    return {
      ...result,
      reason: "unregistered-page",
      detail: `The declared page has no host-compiled view registered for ${page.view}.`,
    }
  return result
}

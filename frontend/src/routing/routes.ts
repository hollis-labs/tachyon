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

// Bounded whole-segment names; page queries never participate in identity.
// Static path syntax is preserved, with internal empty segments refused.
export function validPattern(pattern: string): boolean {
  if (!pattern.startsWith("/") || pattern.length > 128) return false
  const names = new Set<string>()
  return pattern
    .slice(1)
    .split("/")
    .every((segment, index) => {
      if (segment.startsWith(":")) {
        if (!/^:[a-z][a-z0-9_]*$/.test(segment) || names.has(segment)) return false
        names.add(segment)
        return true
      }
      return (index === 0 ? /^[a-z0-9][a-z0-9_-]*$/ : /^[a-z0-9_-]+$/).test(segment)
    })
}

export function matchRoute(pattern: string, path: string): Record<string, string> | null {
  if (!validPattern(pattern) || !parseLocation(path).valid || path.includes("?")) return null
  const expected = pattern.slice(1).split("/")
  const actual = path.slice(1).split("/")
  if (expected.length !== actual.length) return null
  const params: Record<string, string> = Object.create(null)
  for (let index = 0; index < expected.length; index++) {
    const segment = expected[index]
    if (segment.startsWith(":")) params[segment.slice(1)] = decodeURIComponent(actual[index])
    else if (segment !== actual[index]) return null
  }
  return params
}

export function routePath(pattern: string, params: Record<string, string> = {}): string {
  if (!validPattern(pattern)) throw new Error("Invalid route pattern")
  return pattern
    .split("/")
    .map((segment) => {
      if (!segment.startsWith(":")) return segment
      const name = segment.slice(1)
      const value = Object.hasOwn(params, name) ? params[name] : undefined
      if (!value || value === "." || value === ".." || hasControl(value))
        throw new Error(`Missing or invalid route parameter: ${name}`)
      // Preserve IDs that coincide with static siblings, including "board".
      return encodeURIComponent(value).replace(
        /^[A-Za-z0-9_.!~*'()-]/,
        (character) => `%${character.charCodeAt(0).toString(16).toUpperCase()}`,
      )
    })
    .join("/")
}

function compareSpecificity(a: RoutePage, b: RoutePage) {
  const left = a.route.split("/")
  const right = b.route.split("/")
  for (let index = 1; index < left.length; index++) {
    const difference = Number(left[index].startsWith(":")) - Number(right[index].startsWith(":"))
    if (difference) return difference
  }
  return 0
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

// Current host adapter only: the existing Work detail component is addressable
// only while its work-ops parent declaration is admitted. This never admits a
// route from the compiled-owner attribution table or manufactures v2 wire data.
export function withWorkDetail(catalog: RouteCatalog): RouteCatalog {
  const parent = catalog.pages.find((page) => page.route === "/work" && page.owner === "work-ops")
  const item = catalog.items.find((item) => item.page === parent?.id)
  if (!parent || !item || catalog.pages.some((page) => page.route === "/work/:id")) return catalog
  const id = "host:work-detail"
  return {
    ...catalog,
    pages: [
      ...catalog.pages,
      {
        id,
        route: "/work/:id",
        title: "Task details",
        view: parent.view,
        owner: parent.owner,
        hidden: true,
        requiresVerbs: [...(parent.requiresVerbs ?? []), "work_read"],
      },
    ],
    subviews: [...catalog.subviews, { id, parent: item.id, page: id, label: "Task details" }],
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
  const concreteRoot = decodeURIComponent(location.path.slice(1).split("/")[0])
  // Literal segments outrank named params. Matching is whole-path, never prefix.
  const matches = catalog.pages
    .map((page) => ({ page, params: matchRoute(page.route, location.path) }))
    .filter(({ page, params }) => {
      if (params === null) return false
      if (["dashboard", "plugin-recovery"].includes(concreteRoot))
        return page.id === "host-dashboard" && page.route === "/dashboard" && !page.owner
      return concreteRoot !== "settings" || page.owner === "config-ops"
    })
    .sort((a, b) => compareSpecificity(a.page, b.page))
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
        return routePath(parentPage.route, params ?? {})
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

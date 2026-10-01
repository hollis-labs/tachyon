export interface NavGroup {
  id: string
  label: string
  icon?: string
  priority?: number
}
export interface NavItem {
  id: string
  label: string
  group: string
  route: string
  requires_verb?: string
  priority?: number
}
export interface Navigation {
  groups?: NavGroup[]
  items?: NavItem[]
}

export async function fetchNavigation(): Promise<Navigation> {
  const response = await fetch("/api/nav", { headers: { Accept: "application/json" } })
  if (!response.ok) throw new Error(`Failed to load navigation (HTTP ${response.status})`)
  const data: Navigation = await response.json()
  if (
    !data ||
    (data.groups !== undefined && !Array.isArray(data.groups)) ||
    (data.items !== undefined && !Array.isArray(data.items))
  ) {
    throw new Error("Invalid navigation response")
  }
  return data
}

export function visibleNavigation(
  nav: Navigation,
  has: (verb: string) => boolean,
  hasPage: (route: string) => boolean,
): NavItem[] {
  const groups = new Map((nav.groups ?? []).map((group) => [group.id, group]))
  return (nav.items ?? [])
    .filter(
      (item) =>
        !!item &&
        typeof item.route === "string" &&
        hasPage(item.route) &&
        (!item.requires_verb || has(item.requires_verb)),
    )
    .sort(
      (a, b) =>
        (groups.get(a.group)?.priority ?? 1000) - (groups.get(b.group)?.priority ?? 1000) ||
        a.group.localeCompare(b.group) ||
        (a.priority ?? 1000) - (b.priority ?? 1000) ||
        a.id.localeCompare(b.id),
    )
}

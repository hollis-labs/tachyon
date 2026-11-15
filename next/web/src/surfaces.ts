// The shell picks a page by exact surface route (app-shell 0.11.0 has no prefix routes). Tachyon's
// areas have deep links (an item, a session, a PR) that the pages resolve themselves from the URL,
// so the shell is told the area's own route rather than the full path (the pattern Tangent uses).

export const LANDING = "/planner"

/** Paths that belong to an area without sharing its prefix. */
const ALIASES: ReadonlyArray<readonly [prefix: string, area: string]> = [["/pr", "/repos"]]

const within = (path: string, prefix: string) => path === prefix || path.startsWith(`${prefix}/`)

/**
 * Old URLs that must keep working. Returns the path to replace the URL with, or null.
 * `/` and `/tasks*` land on the planner; `/prs/inbox` became `/repos`.
 */
export function legacyRedirect(path: string): string | null {
  if (path === "/" || path === "") return LANDING
  if (within(path, "/tasks")) return LANDING
  if (within(path, "/prs")) return "/repos"
  return null
}

/**
 * The surface route a browser path belongs to; any other path is passed through unchanged.
 * `routes` are the manifest's surface routes (next/cmd/tachyon/ui/app.json), so there is one list.
 */
export function surfacePath(pathname: string, routes: readonly string[]): string {
  const path = pathname.length > 1 ? pathname.replace(/\/+$/, "") : pathname
  // Longest first, so a nested surface route wins over its parent.
  const areas = [...routes].sort((a, b) => b.length - a.length)
  for (const area of areas) if (area !== "/" && within(path, area)) return area
  for (const [prefix, area] of ALIASES) if (within(path, prefix)) return area
  return path
}

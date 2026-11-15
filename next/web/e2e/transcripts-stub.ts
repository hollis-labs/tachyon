/**
 * The Transcripts contract stub (G7a): the routes the carried transcripts page calls, answered with the old contract
 * stub's fixtures (internal/transcriptsapi/fixtures at main d5db765, in e2e/transcripts-fixtures, with a project
 * name made a placeholder). The transcripts backend is not built yet; its slice is written against this route list
 * (next/web/CONVERSION_NOTES.md, "Transcripts"). Errors are flat `{code, message}`.
 *
 * `store.refuse` makes every read answer 403 `scope_required`, for the access-denied path.
 */
import { readFileSync } from "node:fs"
import type { Page, Route } from "@playwright/test"

// biome-ignore lint/suspicious/noExplicitAny: fixture JSON, served as read
const fixture = (name: string): any =>
  JSON.parse(readFileSync(new URL(`./transcripts-fixtures/${name}.json`, import.meta.url), "utf8"))

export const TRANSCRIPTS = "/api/plugins/console/v1/transcripts"

export function transcriptsSeed() {
  return { refuse: false }
}

export async function transcriptsStub(page: Page) {
  const store = transcriptsSeed()
  const json = (route: Route, status: number, body: unknown) =>
    route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) })
  const sessions: Record<string, unknown> = {}
  for (const name of ["session_a", "session_b", "session_c"]) {
    const s = fixture(name)
    sessions[s.session.id] = s
  }
  await page.route(`**${TRANSCRIPTS}/**`, async (route) => {
    const req = route.request()
    const url = new URL(req.url())
    const path = url.pathname.slice(TRANSCRIPTS.length)
    if (req.method() !== "GET")
      return json(route, 405, { code: "method_not_allowed", message: "Transcripts are read-only." })
    if (store.refuse)
      return json(route, 403, {
        code: "scope_required",
        message: "This needs tachyon.transcripts.read.",
      })
    if (path === "/snapshot") return json(route, 200, fixture("snapshot_live"))
    if (path === "/status") return json(route, 200, fixture("status"))
    if (path === "/stats") return json(route, 200, fixture("stats"))
    if (path === "/search") {
      const mode = url.searchParams.get("mode") ?? "text"
      const q = url.searchParams.get("q") ?? ""
      if (!q.trim()) return json(route, 200, fixture("search_empty"))
      return json(
        route,
        200,
        fixture(
          `search_${mode === "semantic" || mode === "hybrid" || mode === "summary" ? mode : "text"}`,
        ),
      )
    }
    const m = path.match(/^\/sessions\/([^/]+)(\/find|\/errors)?$/)
    if (m) {
      const id = decodeURIComponent(m[1])
      if (!sessions[id])
        return json(route, 404, { code: "not_found", message: "No such transcript." })
      if (m[2] === "/find") return json(route, 200, fixture("find"))
      if (m[2] === "/errors") return json(route, 200, fixture("errors"))
      return json(route, 200, sessions[id])
    }
    return json(route, 404, { code: "not_found", message: "no such route" })
  })
  return { store }
}

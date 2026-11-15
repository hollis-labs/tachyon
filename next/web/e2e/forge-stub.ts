/**
 * The Repos (forge) contract stub (G7b): the routes the carried PR inbox and PR workspace call, answered with the old
 * contract stub's fixtures (internal/forgeapi/fixtures at main d5db765, in e2e/forge-fixtures, with a repo name made a
 * placeholder), plus the console state store and quiet answers for the ConsoleProvider's live sources. The forge
 * backend is not built yet; its slice is written against this route list (next/web/CONVERSION_NOTES.md, "Repos").
 * Errors are flat `{code, message}`.
 *
 * The confirm-gated writes (comment, review, Assign) never reach these routes in the port, as in the old app: the
 * identity seam's step-up is not served, so they are refused first (see tests/repos.spec.ts).
 */
import { readFileSync } from "node:fs"
import type { Page, Route } from "@playwright/test"

// biome-ignore lint/suspicious/noExplicitAny: fixture JSON, served as read
const fixture = (name: string): any =>
  JSON.parse(readFileSync(new URL(`./forge-fixtures/${name}.json`, import.meta.url), "utf8"))

export const CONSOLE = "/api/plugins/console/v1"
export const FORGE = `${CONSOLE}/forge`

export function forgeSeed() {
  return { state: fixture("state") as Record<string, unknown>[] }
}

export async function forgeStub(page: Page) {
  const store = forgeSeed()
  const json = (route: Route, status: number, body: unknown) =>
    route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) })
  const fail = (route: Route, status: number, code: string, message: string) =>
    json(route, status, { code, message })
  const byPr = (name: string, key: string) => (fixture(name) as Record<string, unknown>)[key]

  await page.route(
    /\/api\/plugins\/console\/v1\/(forge\/|console\/state|activity\/agents$|tether\/events$|catalog\/agents$)/,
    async (route) => {
      const req = route.request()
      const url = new URL(req.url())
      const method = req.method()
      const path = url.pathname.slice(CONSOLE.length)

      // The ConsoleProvider's live sources: nothing new in the stub.
      if (path === "/activity/agents") return json(route, 200, { agents: [] })
      if (path === "/tether/events") return json(route, 200, { events: [], next_cursor: null })
      // The Assign dialog's agents (the Tether catalog).
      if (path === "/catalog/agents")
        return json(route, 200, {
          agents: [
            {
              id: "builder-reviewer",
              name: "Builder Reviewer",
              urn: "msg://agent/agent-mux/agt_e2e_reviewer",
              enabled: true,
            },
          ],
        })

      // The console state store (triage state of inbox items).
      if (path === "/console/state" && method === "GET") return json(route, 200, store.state)
      const st = path.match(/^\/console\/state\/(.+)$/)
      if (st && method === "PUT") {
        const key = decodeURIComponent(st[1])
        const row = { key, ...(req.postDataJSON() ?? {}), updatedAt: Date.now(), version: 1 }
        store.state = [...store.state.filter((r) => r.key !== key), row]
        return json(route, 200, row)
      }

      const f = path.slice("/forge".length)
      if (method === "GET" && f === "/pulls/pending") return json(route, 200, fixture("pending"))
      if (method === "GET" && f === "/status") return json(route, 200, fixture("status"))
      if (method === "GET" && f === "/viewer") return json(route, 200, fixture("viewer"))
      if (method === "GET" && f.startsWith("/audit")) return json(route, 200, fixture("audit"))
      const pr = f.match(/^\/repos\/([^/]+)\/([^/]+)\/([^/]+)\/pulls\/(\d+)(\/[a-z/-]+)?$/)
      if (pr) {
        const [, host, owner, name, num, tail = ""] = pr
        const key = `${host}:${owner}/${name}#${num}`
        if (!byPr("pulls", key)) return fail(route, 404, "not_found", "No such pull request.")
        if (method === "GET") {
          const name2 = {
            "": "pulls",
            "/diff": "diffs",
            "/commits": "commits",
            "/comments": "comments",
            "/files": "files",
            "/checks": "checks",
            "/merge-preflight": "preflight",
          }[tail]
          if (name2)
            return json(
              route,
              200,
              byPr(name2, key) ?? (name2 === "preflight" ? fixture("preflight_pending") : []),
            )
          if (tail === "/review-draft")
            return json(route, 200, { ...fixture("review_draft"), pr_key: key })
        }
        if (method === "POST" && tail === "/comments")
          return json(route, 201, fixture("comment_created"))
        if (method === "POST" && tail === "/reviews/validate")
          return json(route, 200, fixture("review_validate"))
        if (method === "POST" && tail === "/reviews")
          return json(route, 201, fixture("review_created"))
        if (method === "PUT" && tail === "/review-draft")
          return json(route, 200, { ...fixture("review_draft"), pr_key: key })
        if (method === "DELETE" && tail === "/review-draft") return route.fulfill({ status: 204 })
        if (method === "PUT" && tail === "/merge") return json(route, 200, fixture("merge_ok"))
        if (method === "PUT" && tail === "/assignees") return json(route, 200, byPr("pulls", key))
      }
      return fail(route, 404, "not_found", "no such route")
    },
  )
  return { store }
}

/**
 * The Builder, Registry and team-operations contract stub (G6): the routes the carried builder, registry and
 * Agent Ops Teams pages call, answered in the old build's shapes. The builder/catalog backend is not built yet;
 * its slice is written against this route list (next/web/CONVERSION_NOTES.md, "Builder and Registry").
 * The builder/* and registry/* answers are the old contract stub's fixtures (internal/builderapi/fixtures at main
 * 4b5e397, in e2e/builder-fixtures, with a host name made a placeholder); the data-verb envelopes
 * (`{version: 1, provider, verb, data}`) follow internal/dataverbs. Errors are flat `{code, message}`.
 *
 * `store.refuseChanges` makes change creation answer 409 `conflict`, for the error path.
 */
import { readFileSync } from "node:fs"
import type { Page, Route } from "@playwright/test"

// biome-ignore lint/suspicious/noExplicitAny: fixture JSON, served as read
const fixture = (name: string): any =>
  JSON.parse(readFileSync(new URL(`./builder-fixtures/${name}.json`, import.meta.url), "utf8"))

export const CONSOLE = "/api/plugins/console/v1"
const AT = "2026-11-14T12:00:00Z"

const team = (id: string, name: string, slots: string[]) => ({
  id,
  kind: "team",
  ownership: "tachyon",
  name,
  revision: 1,
  definition: { slots: slots.map((n) => ({ name: n })) },
})

export function builderSeed() {
  return {
    refuseChanges: false,
    teams: [
      team("team-delivery", "Delivery", ["lead", "reviewer"]),
      team("team-docs", "Docs", ["writer"]),
    ],
    actions: {
      "team-delivery": [
        { id: "act-1", verb: "teams.form", object_revision: 1, state: "succeeded", created_at: AT },
      ],
    } as Record<string, unknown[]>,
    roster: {
      observed_at: AT,
      runs: [
        {
          run: { id: "run-1", status: "active" },
          roster_version: 1,
          members: [
            { id: "m-1", slot: "lead", status: "active", actor: "agt_e2e_lead", kind: "agent" },
            {
              id: "m-2",
              slot: "reviewer",
              status: "active",
              actor: "agt_e2e_reviewer",
              kind: "agent",
            },
          ],
        },
      ],
    },
  }
}

export async function builderStub(page: Page) {
  const store = builderSeed()
  const json = (route: Route, status: number, body: unknown) =>
    route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) })
  const fail = (route: Route, status: number, code: string, message: string) =>
    json(route, status, { code, message })
  const verb = (route: Route, provider: string, name: string, data: unknown) =>
    json(route, 200, { version: 1, provider, verb: name, data })
  const changes = fixture("changes") as Record<string, Record<string, unknown>>
  const keyed = (name: string, id: string) => (fixture(name) as Record<string, unknown>)[id]

  await page.route(
    /\/api\/plugins\/console\/v1\/(builder|registry|data\/providers\/(tachyon|tether)\/verbs)\/|\/api\/plugins\/console\/v1\/catalog\/(imported-teams|team-policy)$/,
    async (route) => {
      const req = route.request()
      const url = new URL(req.url())
      const method = req.method()
      const path = url.pathname.slice(CONSOLE.length)
      const input = () =>
        method === "GET"
          ? JSON.parse(url.searchParams.get("input") ?? "{}")
          : (req.postDataJSON() ?? {})
      const parts = path.split("/").filter(Boolean).map(decodeURIComponent)

      // Data verbs: owned records (tachyon) and team actions (tether).
      if (parts[0] === "data") {
        const [, , provider, , name] = parts
        const args = input()
        if (provider === "tachyon") {
          if (name === "objects.list")
            return verb(route, provider, name, { items: args.kind === "team" ? store.teams : [] })
          if (name === "objects.get") {
            const row = store.teams.find((t) => t.id === args.id)
            return row
              ? verb(route, provider, name, row)
              : fail(route, 404, "not_found", "No such record.")
          }
          if (name === "objects.history") return verb(route, provider, name, { items: [] })
          return fail(
            route,
            503,
            "not_configured",
            "Owned record writes are not available in this build yet.",
          )
        }
        if (name === "actions.list")
          return verb(route, provider, name, { items: store.actions[args.object_id] ?? [] })
        if (name === "teams.roster") return verb(route, provider, name, store.roster)
        return fail(
          route,
          503,
          "not_configured",
          "Team actions reach Tether; not available in this build yet.",
        )
      }
      if (path === "/catalog/imported-teams")
        return json(route, 200, { teams: [], source: "catalog", imported_at: AT })
      if (path === "/catalog/team-policy")
        return json(route, 200, {
          content: [
            "version: 1",
            "launches:",
            "  - launch: builder-reviewer-tachyon",
            "    runtime: claude",
            "    definition:",
            "      id: builder-reviewer",
            '      revision: "1"',
            `      digest: sha256:${"a".repeat(64)}`,
          ].join("\n"),
        })

      // registry/projects
      if (parts[0] === "registry") {
        const [, , id, tail] = parts
        if (!id && method === "GET") return json(route, 200, fixture("registry_projects"))
        if (!id && method === "POST")
          return fail(route, 503, "not_configured", "Registry writes reach Tether.")
        const project = keyed("registry_project_details", id)
        if (!project) return fail(route, 404, "not_found", "No such project.")
        if (!tail && method === "GET") return json(route, 200, project)
        if (!tail && method === "PATCH")
          return json(route, 200, { ...(project as object), ...input() })
        return fail(route, 503, "not_configured", "Registry writes reach Tether.")
      }

      // builder/*
      const [, area, id, tail] = parts
      if (method === "GET" && area === "summary") return json(route, 200, fixture("summary"))
      if (method === "GET" && area === "capabilities")
        return json(route, 200, fixture("capabilities").handoff)
      if (area === "agents" && method === "GET") {
        if (!id)
          return json(
            route,
            200,
            url.searchParams.get("cursor") ? fixture("agents_page2") : fixture("agents"),
          )
        const name = {
          undefined: "agent_details",
          resolved: "agent_resolved",
          used_by: "agent_used_by",
          checks: "agent_checks",
          history: "agent_history",
        }[String(tail)]
        const body = name && keyed(name, id)
        return body ? json(route, 200, body) : fail(route, 404, "not_found", "No such agent.")
      }
      if (area === "launches") {
        const launches = fixture("launches")
        if (method === "GET" && !id) return json(route, 200, launches)
        const launch = launches.launches.find((l: { id: string }) => l.id === id)
        if (!launch) return fail(route, 404, "not_found", "No such launch.")
        if (method === "GET" && !tail) return json(route, 200, launch)
        if (method === "POST" && tail === "launch")
          return json(route, 201, fixture("launch_started"))
      }
      if (area === "library" && method === "GET") {
        const [, , kind, item, sub] = parts
        if (!item) return json(route, 200, fixture(`library_${kind}`))
        const name = {
          undefined: "library_detail",
          used_by: "library_used_by",
          history: "library_history",
        }[String(sub)]
        const body = name && keyed(name, `${kind}/${item}`)
        return body
          ? json(route, 200, body)
          : fail(route, 404, "not_found", "No such library item.")
      }
      if (area === "changes") {
        if (method === "GET" && !id) return json(route, 200, { changes: Object.values(changes) })
        if (method === "POST" && !id) {
          if (store.refuseChanges)
            return fail(
              route,
              409,
              "conflict",
              "The catalog changed since you opened it. Reload and review again.",
            )
          return json(route, 201, { ...changes.chg_draft, ...input() })
        }
        const change = changes[id]
        if (!change) return fail(route, 404, "not_found", "No such change.")
        if (method === "GET" && !tail) return json(route, 200, change)
        if (method === "GET" && tail === "bundle") return json(route, 200, fixture("change_bundle"))
        if (method === "POST") return json(route, 200, change)
      }
      return fail(route, 404, "not_found", "no such route")
    },
  )
  return { store }
}

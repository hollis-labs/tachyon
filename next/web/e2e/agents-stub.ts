/**
 * The Agent Ops contract stub (G5): the routes the carried Agent Ops and Needs you pages call, answered in the
 * shapes of the old build. The agents backend is not built yet; its slice is written against this route list
 * (next/web/CONVERSION_NOTES.md, "Agent Ops"). The agents/* answers are the old contract stub's fixtures
 * (internal/agentsapi/fixtures at main 99c7b88, in e2e/agents-fixtures); seats, ceremony, needs-you and the
 * launch options follow the old handlers' types. Errors are flat `{code, message}`.
 *
 * `store.refuseControls` makes nudge and restart answer 409 `conflict`, for the control error path.
 */
import { readFileSync } from "node:fs"
import type { Page, Route } from "@playwright/test"

// biome-ignore lint/suspicious/noExplicitAny: fixture JSON, served as read
const fixture = (name: string): any =>
  JSON.parse(readFileSync(new URL(`./agents-fixtures/${name}.json`, import.meta.url), "utf8"))
const busEvents = fixture("bus_events")
const contextSeries = fixture("context_series")
const controls = fixture("controls")
const history = fixture("history")
const nudgeDelivered = fixture("nudge_delivered")
const restartRequested = fixture("restart_requested")
const sessionEvents = fixture("session_events")
const sessionEventsPage2 = fixture("session_events_page2")
const sessions = fixture("sessions")
const settings = fixture("settings")
const tokens = fixture("tokens")

export const CONSOLE = "/api/plugins/console/v1"
export const AGENTS = `${CONSOLE}/agents`
const AT = "2026-11-14T12:00:00Z"

export function agentsSeed() {
  return {
    refuseControls: false,
    seats: [
      {
        agent_urn: "msg://agent/agent-mux/agt_e2e_builder",
        handle: "@builder-1",
        name: "Builder One",
        effective: "blocked",
        declared: {
          state: "blocked",
          reason: "needs the deploy token",
          ref: "task:CW-E2E-0001",
          owner: "@chrispian",
          expires_at: null,
          set_by: "msg://agent/agent-mux/agt_e2e_builder",
          at: AT,
        },
        live: {
          session_id: "sess_work",
          state: "waiting",
          needs_you: "approval",
          last_activity_at: Date.parse(AT),
        },
      },
      {
        agent_urn: "msg://agent/agent-mux/agt_e2e_reviewer",
        handle: "@reviewer",
        name: "Reviewer",
        effective: "available",
        declared: {
          state: "available",
          reason: "",
          ref: "",
          owner: "",
          expires_at: null,
          set_by: "x",
          at: AT,
        },
        live: null,
      },
    ],
    needsYou: {
      checked_at: AT,
      sources: {
        ask: { ok: true },
        mail: { ok: true },
        status: { ok: true },
        chat: { ok: true },
        approvals: { ok: true },
      },
      items: [
        {
          id: "ny-1",
          source: "status",
          kind: "seat.blocked",
          why: "unblock",
          title: "needs the deploy token",
          from: "@builder-1",
          ref: "task:CW-E2E-0001",
          since: AT,
          critical: false,
        },
        {
          id: "ny-2",
          source: "approvals",
          kind: "approval",
          why: "decide",
          title: "Approvals waiting",
          from: "approvals",
          since: AT,
          critical: true,
          detail: "2 requests",
          link: "/approvals",
        },
      ],
    },
    ceremony: {
      checked_at: AT,
      window_days: 7,
      gates: { total: 12, reviews: 5, approvals: 4, checkpoints: 2, asks: 1 },
      median_wait_seconds: 1500,
      wait_samples: 9,
      stalled: [
        { id: "CW-E2E-0002", title: "Port the seats view", status: "review", updated_at: AT },
      ],
      stalled_total: 1,
      pending_checkpoints: [],
      pending_checkpoints_total: 0,
      activity: 420,
      done: 6,
      previous: { day: "2026-11-07", activity: 380, done: 5, gates: 10 },
      comparison: "steady",
      sources: { torque: { ok: true } },
    },
  }
}

export async function agentsStub(page: Page) {
  const store = agentsSeed()
  const json = (route: Route, status: number, body: unknown) =>
    route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) })
  const fail = (route: Route, status: number, code: string, message: string) =>
    json(route, status, { code, message })
  const session = (id: string) => sessions.sessions.find((s: { id: string }) => s.id === id)

  await page.route(
    /\/api\/plugins\/console\/v1\/(agents\/|ceremony$|needs-you$|catalog\/launches$|tether\/)/,
    async (route) => {
      const req = route.request()
      const url = new URL(req.url())
      const method = req.method()
      const path = url.pathname.slice(CONSOLE.length)
      let m: RegExpMatchArray | null = null
      const at = (re: RegExp) => {
        m = path.match(re)
        return m !== null
      }
      if (method === "GET" && path === "/ceremony") return json(route, 200, store.ceremony)
      if (method === "GET" && path === "/needs-you") return json(route, 200, store.needsYou)
      if (method === "GET" && path === "/catalog/launches")
        return json(route, 200, {
          launches: [
            { id: "launch-e2e", name: "Reviewer", agent_id: "agt_e2e_reviewer", enabled: true },
          ],
        })
      if (method === "GET" && path === "/tether/workers") return json(route, 200, { workers: [] })
      // Launching reaches Tether; outbound effects are out of this slice.
      if (path.startsWith("/tether/"))
        return fail(route, 503, "not_configured", "Launching is not available in this build yet.")
      if (method === "GET" && path === "/agents/sessions") return json(route, 200, sessions)
      if (method === "GET" && path === "/agents/settings") return json(route, 200, settings)
      if (method === "PUT" && path === "/agents/settings")
        return json(route, 200, { ...settings, ...(req.postDataJSON() ?? {}) })
      if (method === "GET" && path === "/agents/seats")
        return json(route, 200, { seats: store.seats })
      if (method === "GET" && path === "/agents/bus/events") return json(route, 200, busEvents)
      if (at(/^\/agents\/sessions\/([^/]+)(\/[a-z-]+)?$/)) {
        const [, rawId, tail = ""] = m as unknown as string[]
        const id = decodeURIComponent(rawId)
        if (!session(id)) return fail(route, 404, "not_found", "No such session.")
        if (method === "GET" && tail === "") return json(route, 200, session(id))
        if (method === "GET" && tail === "/events")
          return json(
            route,
            200,
            url.searchParams.get("before_seq") ? sessionEventsPage2 : sessionEvents,
          )
        if (method === "GET" && tail === "/context-series") return json(route, 200, contextSeries)
        if (method === "GET" && tail === "/tokens") return json(route, 200, tokens)
        if (method === "GET" && tail === "/history") return json(route, 200, history)
        if (method === "POST" && (tail === "/nudge" || tail === "/restart")) {
          if (store.refuseControls)
            return fail(
              route,
              409,
              "conflict",
              "The session is not at a turn boundary. Try again when it is.",
            )
          return json(route, 202, tail === "/nudge" ? nudgeDelivered : restartRequested)
        }
      }
      if (at(/^\/agents\/controls\/([^/]+)(\/skip)?$/)) {
        const [, id, skip] = m as unknown as string[]
        const known = (controls as Record<string, unknown>)[decodeURIComponent(id)]
        if (method === "GET" && !skip)
          return known ? json(route, 200, known) : json(route, 200, nudgeDelivered)
        if (method === "POST" && skip) return json(route, 200, known ?? nudgeDelivered)
      }
      return fail(route, 404, "not_found", "no such route")
    },
  )
  return { store }
}

import type { Page, Route } from "@playwright/test"

/**
 * A contract stub for the planner API (`/api/plugins/console/v1/planner/*`, B1 contract: paths unchanged) until B5
 * serves it from the next/ binary. Identity, sessions and the shell stay real; only these paths are answered here,
 * from an in-memory store seeded per test. Shapes follow the old backend (internal/planner): schema ids
 * `tachyon/planner/<db>@1`, enums from internal/planner/schema/*.json, writes checked against `expected_revision`
 * (409 `conflict` with `details.current` on a mismatch), errors in the flat `{code, message, details?}` body.
 * Paths the stub does not serve answer 404 `not_found`, which the UI treats as a feature that is absent.
 */

export const BASE = "/api/plugins/console/v1/planner"
const DBS = ["themes", "workstreams", "roadmap", "ideas", "decisions", "risks", "inbox"] as const
type Db = (typeof DBS)[number]

const ENUMS: Record<Db, Record<string, string[]>> = {
  themes: { status: ["proposed", "ready", "approved", "paused", "done", "archived"] },
  workstreams: {
    status: ["active", "paused", "planned", "parked", "done", "unreviewed"],
    phase: ["landing", "adoption", "planning", "maintenance", "unknown"],
  },
  roadmap: {
    status: ["draft", "planned", "active", "completed", "deferred", "cancelled", "archived"],
    horizon: ["now", "next", "later", "someday"],
  },
  ideas: { status: ["new", "discussing", "decided", "parked", "dropped"] },
  decisions: { status: ["needs-decision", "decided", "deferred", "superseded", "reversed"] },
  risks: {
    status: ["open", "mitigating", "accepted", "closed"],
    severity: ["low", "medium", "high"],
  },
  inbox: { status: ["new", "triaged", "promoted", "dismissed"] },
}

export interface StubItem {
  id: string
  title: string
  rev: number
  status: string
  [field: string]: unknown
}

/** A UUID for the stub's seed data (stable per db and index). */
export const seedId = (db: Db, i: number) =>
  `0190${String(DBS.indexOf(db)).padStart(4, "0")}-0000-7000-8000-${String(i).padStart(12, "0")}`

const DAY = "2026-11-14"

function seed(): Record<Db, StubItem[]> {
  const make = (db: Db, rows: [string, string, Record<string, unknown>?][]) =>
    rows.map(([title, status, extra], i) => ({
      id: seedId(db, i + 1),
      title,
      rev: 1,
      status,
      created: DAY,
      updated: DAY,
      tldr: `${title}: seeded by the e2e contract stub.`,
      ...extra,
    }))
  return {
    themes: make("themes", [
      ["Port the planner", "approved", { order: 1 }],
      ["Retire the old frontend", "ready", { order: 2 }],
      ["Explore offline mode", "proposed"],
    ]),
    workstreams: make("workstreams", [["Substrate migration", "active", { phase: "landing" }]]),
    roadmap: make("roadmap", [["Planner on Substrate", "active", { horizon: "now" }]]),
    ideas: make("ideas", [["Keyboard-first triage", "new"]]),
    decisions: make("decisions", [
      ["Keep react-router as the host router", "needs-decision", { options: [] }],
    ]),
    risks: make("risks", [["Data loss at cutover", "open", { severity: "high", kind: "risk" }]]),
    inbox: make("inbox", [["Link from a chat thread", "new", { kind: "link" }]]),
  }
}

const section = (status: string) =>
  status === "approved"
    ? "in_flight"
    : status === "ready"
      ? "queued"
      : status === "paused"
        ? "parked"
        : status === "done"
          ? "landed"
          : "proposed"

/** Installs the stub on `page` and returns its store, so a test can seed and inspect writes. */
export async function plannerStub(page: Page) {
  const store = seed()
  const writes: { method: string; path: string; body: unknown }[] = []
  const json = (route: Route, status: number, body: unknown) =>
    route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) })
  const fail = (route: Route, status: number, code: string, message: string, details?: unknown) =>
    json(route, status, details === undefined ? { code, message } : { code, message, details })

  await page.route(`**${BASE}/**`, async (route) => {
    const req = route.request()
    const url = new URL(req.url())
    const path = url.pathname.slice(BASE.length)
    const method = req.method()
    const parts = path.split("/").filter(Boolean)
    const body = req.postData() ? JSON.parse(req.postData() as string) : undefined
    if (method !== "GET") {
      writes.push({ method, path, body })
      if (!req.headers()["x-requested-with"])
        return fail(route, 403, "csrf", "missing X-Requested-With")
    }
    const db = parts[0] as Db

    if (method === "GET" && path === "/databases")
      return json(
        route,
        200,
        DBS.map((name) => ({
          name,
          prefix: "",
          schema: `tachyon/planner/${name}@1`,
          updated: DAY,
          count: store[name].length,
        })),
      )
    if (method === "GET" && parts[0] === "schema" && DBS.includes(parts[1] as Db)) {
      const d = parts[1] as Db
      const fields: Record<string, unknown> = {
        title: { type: "string" },
        tldr: { type: "string" },
        notes: { type: "string" },
      }
      for (const [k, v] of Object.entries(ENUMS[d])) fields[k] = { enum: v }
      return json(route, 200, {
        db: d,
        id: `tachyon/planner/${d}@1`,
        description: `${d} (contract stub)`,
        required: ["id", "title", "status"],
        fields,
        enums: ENUMS[d],
        link_kinds: ["torque", "pr", "url", "file", "workstream"],
      })
    }
    if (method === "GET" && path === "/board" && url.searchParams.get("scope") === "themes") {
      const keys = ["in_flight", "cleared", "queued", "parked", "landed", "proposed"] as const
      const sections = Object.fromEntries(keys.map((k) => [k, [] as unknown[]]))
      for (const t of store.themes) {
        const s = section(t.status)
        sections[s].push({
          source: "theme",
          db: "themes",
          id: t.id,
          rev: t.rev,
          title: t.title,
          status: t.status,
          section: s,
          updated: `${DAY}T12:00:00Z`,
          order: t.order,
          tldr: t.tldr,
          blocked_count: 0,
          unmet_dependencies: 0,
          progress: { available: true, done: 0, total: 0 },
        })
      }
      const counts = Object.fromEntries(keys.map((k) => [k, sections[k].length]))
      return json(route, 200, {
        scope: "themes",
        generated_at: `${DAY}T12:00:00Z`,
        recent_hours: 168,
        limit: 50,
        sections,
        counts,
        totals: counts,
        notices: [],
        progress: { done: 0, total: 0 },
      })
    }
    if (method === "GET" && path === "/need-you")
      return json(route, 200, { total: 0, by_reason: {}, by_lens: {}, items: [], stale_days: 14 })
    if (method === "GET" && path === "/torque-titles") return json(route, 200, {})
    if (method === "GET" && parts[0] === "themes" && parts[2] === "tasks")
      return json(route, 200, {
        theme_id: parts[1],
        counts: { todo: 0, doing: 0, review: 0, blocked: 0, done: 0, total: 0 },
        lanes: { todo: [], doing: [], review: [], blocked: [], done: [] },
      })
    if (method === "GET" && parts[0] === "themes" && parts[2] === "timeline")
      return json(route, 200, {
        milestones: [],
        momentum: { label: "steady", score: 0, text: "No activity yet" },
        events: [],
        next_before_seq: null,
      })
    if (DBS.includes(db)) {
      const items = store[db]
      if (parts.length === 1 && method === "GET") return json(route, 200, items)
      if (parts.length === 1 && method === "POST") {
        const { id, fields } = body as { id: string; fields: Record<string, unknown> }
        if (!fields?.title)
          return fail(route, 400, "invalid", "title is required", {
            errors: [{ path: "title", message: "required" }],
          })
        const item: StubItem = {
          status: ENUMS[db].status[0],
          ...fields,
          id,
          title: String(fields.title),
          rev: 1,
          created: DAY,
          updated: DAY,
        }
        items.unshift(item)
        return json(route, 201, item)
      }
      const item = items.find((i) => i.id === parts[1])
      if (!item) return fail(route, 404, "not_found", "no such item")
      if (parts.length === 2 && method === "GET") return json(route, 200, item)
      if (parts.length === 2 && method === "PATCH") {
        const { patch, expected_revision } = body as {
          patch: Record<string, unknown>
          expected_revision: number
        }
        if (expected_revision !== item.rev)
          return fail(route, 409, "conflict", "the item changed", { current: item })
        Object.assign(item, patch, { rev: item.rev + 1, updated: DAY })
        return json(route, 200, item)
      }
      if (parts[2] === "links" && method === "GET")
        return json(route, 200, { outbound: [], inbound: [], external: [] })
    }
    return fail(route, 404, "not_found", `the contract stub does not serve ${method} ${path}`)
  })
  // The planner's two SSE streams: an empty stream (the client refetches on its own fallback timer).
  for (const stream of [`**${BASE}/events`, "**/api/plugins/console/stream/events"])
    await page.route(stream, (route) =>
      route.fulfill({ status: 200, contentType: "text/event-stream", body: ": stub\n\n" }),
    )
  return { store, writes }
}

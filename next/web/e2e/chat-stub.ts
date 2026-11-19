import type { Page, Route } from "@playwright/test"

/**
 * A contract stub for the chat routes the chat surface uses (next/web/CONVERSION_NOTES.md lists them; B6 serves
 * them from the next/ binary). Identity, sessions and the shell stay real; these paths are answered from an
 * in-memory store seeded per test. Shapes follow the old backend (internal/chat*), errors are the flat
 * `{code, message, details?}` body, writes need X-Requested-With, plan writes check `expected_revision`
 * (409 `conflict`), a plan delete answers 204. Paths it does not serve answer 404 `not_found`.
 */

export const CHAT = "/api/plugins/console/v1/chat"
export const THREAD = "tether:sess-e2e-1"
export const OTHER_THREAD = "tether:sess-e2e-2"
const T0 = Date.parse("2026-11-14T12:00:00Z")

interface Step {
  id: string
  position: number
  title: string
  status: string
  task_ref: string | null
}
interface Plan {
  id: string
  position: number
  title: string | null
  status: string
  revision: number
  task_ref: string | null
  steps: Step[]
}
interface Message {
  seq: number
  id: string
  role: "operator" | "agent" | "system"
  kind: string
  text: string
  at: number
  meta: null
}

export function chatSeed() {
  // The conversation list keys a roster row by its chat launch id; the API threads attach to it by agent.agent_id.
  const agent = { agent_id: "L_e2e" }
  const thread = (thread_ref: string, title: string, last_preview: string, at: number) => ({
    thread_ref,
    title,
    agent,
    unread: 0,
    last_at: at,
    last_preview,
    blocked_on_user: 0,
    blocked_breakdown: { approvals: 0, questions: 0 },
    pinned: false,
    archived: false,
    plan_progress: null,
  })
  return {
    threads: [
      thread(THREAD, "Port review", "The planner surface is ready for review.", T0 + 60_000),
      thread(OTHER_THREAD, "Release notes", "Drafted the notes.", T0),
    ],
    messages: {
      [THREAD]: [
        {
          seq: 1,
          id: "m_1",
          role: "operator",
          kind: "text",
          text: "How is the port going?",
          at: T0,
          meta: null,
        },
        {
          seq: 2,
          id: "m_2",
          role: "agent",
          kind: "text",
          text: "The planner surface is ready for review.",
          at: T0 + 60_000,
          meta: null,
        },
      ],
      [OTHER_THREAD]: [
        {
          seq: 1,
          id: "m_3",
          role: "agent",
          kind: "text",
          text: "Drafted the notes.",
          at: T0,
          meta: null,
        },
      ],
    } as Record<string, Message[]>,
    plans: {
      [THREAD]: [
        {
          id: "pl_e2e_1",
          position: 0,
          title: "Port the chat surface",
          status: "active",
          revision: 1,
          task_ref: null,
          steps: [
            {
              id: "st_e2e_1",
              position: 0,
              title: "Carry the chat code",
              status: "done",
              task_ref: null,
            },
            {
              id: "st_e2e_2",
              position: 1,
              title: "Write the contract stub",
              status: "doing",
              task_ref: null,
            },
          ],
        },
      ],
    } as Record<string, Plan[]>,
    workspace: {
      [THREAD]: [
        {
          id: "ws_e2e_1",
          type: "file",
          name: "port-notes.md",
          mime: "text/markdown",
          size_bytes: 40,
          url: null,
          ref: null,
          shared: true,
          added_by: "agent",
        },
      ],
    } as Record<string, unknown[]>,
    agents: [
      {
        launch_id: "L_e2e",
        agent_id: "agt_e2e_reviewer",
        display_name: "Reviewer",
        channel: "e2e",
        session: {
          id: "sess-e2e-1",
          state: "running",
          last_activity_at: "2026-11-14T12:01:00Z",
          at_turn_boundary: true,
        },
      },
    ],
  }
}

/** Plain browser JavaScript (no transpiling): EventSources for the stubbed streams open and stay quiet. */
const QUIET_STREAMS = `(() => {
  const Real = window.EventSource;
  const quiet = /\\/api\\/plugins\\/console\\/(v1\\/chat(\\/threads\\/[^/]+)?\\/events|v1\\/planner\\/events|v1\\/approval-inbox\\/events|v1\\/agents\\/stream|v1\\/builder\\/stream|v1\\/forge\\/stream|stream\\/activity|stream\\/events)/;
  class Quiet extends EventTarget {
    constructor(url) {
      super();
      this.url = String(url); this.withCredentials = false; this.readyState = 0;
      this.onopen = null; this.onmessage = null; this.onerror = null;
      setTimeout(() => { this.readyState = 1; const e = new Event("open"); if (this.onopen) this.onopen(e); this.dispatchEvent(e); }, 0);
    }
    close() { this.readyState = 2; }
  }
  class Stubbed extends Real {
    constructor(url, init) {
      if (quiet.test(String(url))) return new Quiet(url);
      super(url, init);
    }
  }
  window.EventSource = Stubbed;
})();`

/** Installs the stub on `page` and returns its store. */
export async function chatStub(page: Page) {
  const store = chatSeed()
  let ids = 100
  const json = (route: Route, status: number, body: unknown) =>
    route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) })
  const fail = (route: Route, status: number, code: string, message: string, details?: unknown) =>
    json(route, status, details === undefined ? { code, message } : { code, message, details })

  // Live streams: a stubbed response would close at once and the UI would show "Reconnecting". In stub mode the
  // chat and planner streams are quiet open EventSources instead (nothing arrives; the UI reads by request).
  await page.addInitScript({ content: QUIET_STREAMS })

  await page.route(
    /\/api\/plugins\/console\/v1\/(chat|chat-turns|terminal|files|catalog)\//,
    async (route) => {
      const req = route.request()
      const url = new URL(req.url())
      const method = req.method()
      const path = url.pathname.replace("/api/plugins/console/v1", "")
      const parts = path.split("/").filter(Boolean).map(decodeURIComponent)
      const body = req.postData() ? JSON.parse(req.postData() as string) : undefined
      // The Builder's two catalog reads belong to the builder area: its stub (registered later, so it answers
      // first) or, with TACHYON_E2E_BUILDER=backend, the binary.
      if (path === "/catalog/imported-teams" || path === "/catalog/team-policy")
        return route.fallback()
      if (method !== "GET" && !req.headers()["x-requested-with"])
        return fail(route, 403, "csrf", "missing X-Requested-With")

      if (method === "GET" && path === "/chat/threads")
        return json(route, 200, { threads: store.threads, has_more: false })
      if (method === "GET" && path === "/chat/agents")
        return json(route, 200, { agents: store.agents })
      if (method === "GET" && path === "/chat/settings")
        return json(route, 200, { scratchpad_placement: "drawer" })
      if (method === "PUT" && path === "/chat/settings") return json(route, 200, body)
      if (method === "GET" && path === "/chat/outbox") return json(route, 200, [])
      if (method === "GET" && path === "/chat-turns/inbox")
        return json(route, 200, { pending: [], history: [] })
      if (method === "GET" && path === "/terminal/runs") return json(route, 200, { runs: [] })
      if (method === "GET" && path === "/files/roots")
        return json(route, 200, { roots: [], caps: {} })
      if (method === "GET" && path === "/catalog/agents") return json(route, 200, { agents: [] })
      if (method === "GET" && path === "/catalog/launches")
        return json(route, 200, { launches: [] })

      if (parts[0] === "chat" && parts[1] === "threads" && parts[2]) {
        const ref = parts[2]
        const rest = parts.slice(3)
        const thread = store.threads.find((t) => t.thread_ref === ref)
        if (!thread) return fail(route, 404, "not_found", "No such thread.")
        store.messages[ref] ??= []
        const messages = store.messages[ref]
        store.plans[ref] ??= []
        const plans = store.plans[ref]
        const sub = rest.join("/")
        if (sub === "" && method === "PATCH") return json(route, 200, Object.assign(thread, body))
        if (sub === "messages" && method === "GET")
          return json(route, 200, { messages, has_more: false })
        if (sub === "messages" && method === "POST") {
          const text = String(body?.text ?? body?.body ?? "")
          if (!text.trim()) return fail(route, 400, "invalid", "A message needs text.")
          const seq = messages.length + 1
          const id = `m_${ids++}`
          messages.push({
            seq,
            id,
            role: "operator",
            kind: "text",
            text,
            at: Date.now(),
            meta: null,
          })
          Object.assign(thread, { last_preview: text, last_at: Date.now() })
          // B6 (old handler): 202 accepted, with the idempotency key echoed.
          return json(route, 202, {
            id,
            seq,
            idempotency_key: body?.idempotency_key ?? null,
            state: "accepted",
          })
        }
        if (sub === "read" && method === "POST") return json(route, 200, { ok: true })
        if (sub === "stop" && method === "POST")
          return fail(route, 409, "nothing_to_stop", "Nothing is running in this conversation.")
        if (sub === "plan" && method === "GET")
          return fail(route, 404, "not_found", "No such plan, step or thread.")
        if (sub === "plans" && method === "GET") return json(route, 200, { plans })
        // Create plan and reorder take no expected_revision and refuse unknown fields (old handler: 400).
        const only = (allowed: string[]) =>
          Object.keys(body ?? {}).every((k) => allowed.includes(k))
        if (sub === "plans/reorder" && method === "POST") {
          if (!only(["plan_ids"])) return fail(route, 400, "invalid", "Unknown field.")
          const order = (body?.plan_ids ?? []) as string[]
          plans.sort((a, b) => order.indexOf(a.id) - order.indexOf(b.id))
          plans.forEach((p, i) => {
            p.position = i
          })
          return json(route, 200, { plans })
        }
        if (sub === "plans" && method === "POST") {
          if (!only(["title"])) return fail(route, 400, "invalid", "Unknown field.")
          const plan: Plan = {
            id: `pl_${ids++}`,
            position: plans.length,
            title: body?.title ?? null,
            status: "active",
            revision: 1,
            task_ref: null,
            steps: [],
          }
          plans.push(plan)
          return json(route, 201, plan)
        }
        if (rest[0] === "plans" && rest[1]) {
          const plan = plans.find((p) => p.id === rest[1])
          if (!plan) return fail(route, 404, "not_found", "No such plan, step or thread.")
          const expected =
            rest.length === 2
              ? Number(url.searchParams.get("expected_revision"))
              : body?.expected_revision
          if (expected !== undefined && expected !== plan.revision)
            return fail(route, 409, "conflict", "The plan changed; reload it and try again.", {
              current: plan,
            })
          if (rest.length === 2 && method === "DELETE") {
            plans.splice(plans.indexOf(plan), 1)
            return route.fulfill({ status: 204, body: "" })
          }
          if (rest[2] === "steps" && rest.length === 3 && method === "POST") {
            const step: Step = {
              id: `st_${ids++}`,
              position: plan.steps.length,
              title: String(body?.title ?? ""),
              status: "todo",
              task_ref: null,
            }
            plan.steps.push(step)
            plan.revision++
            return json(route, 201, { ...step, plan_revision: plan.revision })
          }
          if (rest[2] === "steps" && rest[3] && rest.length === 4 && method === "PATCH") {
            const step = plan.steps.find((s) => s.id === rest[3])
            if (!step) return fail(route, 404, "not_found", "No such plan, step or thread.")
            Object.assign(step, { status: body?.status ?? step.status })
            plan.revision++
            return json(route, 200, { ...step, plan_revision: plan.revision })
          }
        }
        if (sub === "cards" && method === "GET") return json(route, 200, { cards: [] })
        if (sub === "topics" && method === "GET") return json(route, 200, { topics: [] })
        if (sub === "bookmarks" && method === "GET") return json(route, 200, { bookmarks: [] })
        if (sub === "work" && method === "GET")
          return json(route, 200, { items: [], source: "stub" })
        if (sub === "activity" && method === "GET")
          return json(route, 200, { activity: [], has_more: false })
        if (sub === "approval-rules" && method === "GET") return json(route, 200, { rules: [] })
        if (sub === "workspace" && method === "GET")
          return json(route, 200, { items: store.workspace[ref] ?? [] })
        if (rest[0] === "workspace" && rest[2] === "content" && method === "GET")
          return route.fulfill({
            status: 200,
            contentType: "text/markdown",
            body: "# Port notes\n\nCarried, not redesigned.\n",
          })
        if (sub === "scratchpad" && method === "GET")
          return json(route, 200, { body: "", revision: 0 })
        if (sub === "scratchpad" && method === "PUT")
          return json(route, 200, { body: body?.body ?? "", revision: 1 })
        if (sub === "continue" && method === "GET") return json(route, 200, { continue: false })
        if (sub === "continue" && method === "PUT")
          return json(route, 200, { continue: !!body?.continue })
      }
      return fail(
        route,
        404,
        "not_found",
        `the chat contract stub does not serve ${method} ${path}`,
      )
    },
  )
  return { store }
}

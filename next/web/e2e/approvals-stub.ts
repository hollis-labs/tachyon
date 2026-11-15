import type { Page, Route } from "@playwright/test"

/**
 * A contract stub for the approvals routes the approvals surface uses (route list in next/web/CONVERSION_NOTES.md;
 * B7 serves them from the next/ binary). Identity, sessions and the shell stay real. Shapes follow the old backend
 * (internal/approvals, checked by B7's builder on #750): items carry `revision` and `presented_projection_revision`;
 * present and resolve check both (409 `stale_revision`, the hitl fields under `details`), batch reports a stale item
 * per result, defer checks `expected_revision` only, present leaves the state as it was; a decision sends both back, high-risk items carry `confirm.phrase` (422 `confirmation_required` when the typed
 * phrase is wrong), and a stored secret answers a reference and a fingerprint, never the value. Errors are the flat
 * `{code, message, details?}` body; writes need X-Requested-With. Paths it does not serve answer 404 `not_found`.
 * `store.refuseOperator` makes every decision answer 403 `operator_required`, as for a person without the scope.
 */

export const APPROVALS = "/api/plugins/console/v1/approval-inbox"
const AT = "2026-11-14T12:00:00Z"

function base(
  id: string,
  title: string,
  extra: Record<string, unknown> = {},
  snapshot: Record<string, unknown> = {},
) {
  return {
    item_id: id,
    state: "staged",
    revision: 1,
    presented_projection_revision: 1,
    enqueued_at: AT,
    updated_at: AT,
    batchable: false,
    ...extra,
    request_snapshot: {
      kind: "approval",
      title,
      summary: `${title} (seeded by the e2e contract stub)`,
      request: `${title}.`,
      source: { agent_id: "agt_e2e_reviewer", application_id: "tether" },
      impact: { approve: "The agent may proceed.", deny: "The agent stays blocked." },
      class: "low",
      ...snapshot,
    },
  }
}

export function approvalsSeed() {
  return {
    refuseOperator: false,
    pending: [
      base("req-e2e-low", "Restart the indexer", { batchable: true }),
      base(
        "req-e2e-high",
        "Drop the staging database",
        { confirm: { phrase: "APPROVE 1a2b3c4d" } },
        { class: "high" },
      ),
      base(
        "req-e2e-form",
        "Provide the deploy key",
        {},
        {
          kind: "card.form",
          data: {
            fields: [{ id: "deploy_key", type: "secret", label: "Deploy key", required: true }],
          },
        },
      ),
    ] as Record<string, unknown>[],
    history: [] as Record<string, unknown>[],
    secrets: [] as { item_id: string; field: string; length: number }[],
  }
}

export async function approvalsStub(page: Page) {
  const store = approvalsSeed()
  const json = (route: Route, status: number, body: unknown) =>
    route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) })
  const fail = (route: Route, status: number, code: string, message: string, details?: unknown) =>
    json(route, status, details === undefined ? { code, message } : { code, message, details })
  const find = (id: string) => store.pending.find((i) => i.item_id === id)
  const inbox = () => ({
    pending: store.pending,
    history: store.history,
    synced_at: AT,
    revision: "r1",
    server_now_ms: Date.now(),
  })
  const stale = (route: Route, it: Record<string, unknown>, expected: unknown) =>
    fail(route, 409, "stale_revision", "This request changed. Refresh and review it again.", {
      revision_kind: "item",
      expected_revision: expected,
      actual_revision: it.revision,
      current_state: it.state,
    })

  await page.route(`**${APPROVALS}/**`, async (route) => {
    const req = route.request()
    const url = new URL(req.url())
    const path = url.pathname.slice(APPROVALS.length)
    const parts = path.split("/").filter(Boolean).map(decodeURIComponent)
    const method = req.method()
    const isJson = (req.headers()["content-type"] ?? "").includes("json")
    const body = req.postData() && isJson ? JSON.parse(req.postData() as string) : undefined
    if (method !== "GET" && !req.headers()["x-requested-with"])
      return fail(route, 403, "csrf", "missing X-Requested-With")

    if (method === "GET" && path === "/inbox") return json(route, 200, inbox())
    if (method === "GET" && path === "/history") return json(route, 200, { events: [], facets: {} })
    if (method === "POST" && path === "/secrets") {
      if (store.refuseOperator)
        return fail(route, 403, "operator_required", "Storing a secret needs operator access.")
      const data = req.postData() ?? ""
      store.secrets.push({
        item_id: body?.item_id ?? "form",
        field: body?.field ?? "form",
        length: data.length,
      })
      // 201 with a reference and a fingerprint; never the value.
      return json(route, 201, {
        secret_ref: "secret://e2e/1",
        created_at: AT,
        expires_at: Date.now() + 600_000,
        size: data.length,
        fingerprint: "sha256:9f2c41d0",
      })
    }
    if (method === "POST" && path === "/batch/resolve") {
      if (store.refuseOperator)
        return fail(route, 403, "operator_required", "Batch decisions need operator access.")
      // Each item is checked on its own; a stale one is a result, not an HTTP error.
      const items = (body?.items ?? []) as {
        item_id: string
        expected_revision: number
        presented_projection_revision: number
      }[]
      const results = items.map((i) => {
        const it = find(i.item_id)
        if (
          !it ||
          i.expected_revision !== it.revision ||
          i.presented_projection_revision !== it.presented_projection_revision
        )
          return {
            item_id: i.item_id,
            status: "stale",
            error: { code: "stale_revision", message: "This request changed." },
          }
        store.pending.splice(store.pending.indexOf(it), 1)
        store.history.unshift({ ...it, state: "resolved" })
        return { item_id: i.item_id, status: "resolved" }
      })
      const counts: Record<string, number> = {}
      for (const r of results) counts[r.status] = (counts[r.status] ?? 0) + 1
      return json(route, 200, { batch_id: body?.batch_id, results, counts })
    }
    if (parts[0] === "items" && parts[1]) {
      const it = find(parts[1])
      if (!it) return fail(route, 404, "not_found", "No such request.")
      const sub = parts.slice(2).join("/")
      const revisionsMatch = () =>
        body?.expected_revision === it.revision &&
        body?.presented_projection_revision === it.presented_projection_revision
      if (sub === "" && method === "GET") return json(route, 200, it)
      if (sub === "audit" && method === "GET")
        return json(route, 200, { item_id: it.item_id, events: [] })
      // Present records an audit event and answers the item as it was read: the state does not change.
      if (sub === "present" && method === "POST")
        return revisionsMatch() ? json(route, 200, it) : stale(route, it, body?.expected_revision)
      if (sub === "defer" && method === "POST") {
        if (body?.expected_revision !== it.revision)
          return stale(route, it, body?.expected_revision)
        const until = new Date(Date.parse(AT) + (body?.duration_s ?? 3600) * 1000).toISOString()
        Object.assign(it, { deferred_until: until, revision: Number(it.revision) + 1 })
        return json(route, 200, {
          item_id: it.item_id,
          deferred_until: until,
          revision: it.revision,
        })
      }
      if (sub === "defer" && method === "DELETE") {
        Object.assign(it, { deferred_until: null, revision: Number(it.revision) + 1 })
        return json(route, 200, {
          item_id: it.item_id,
          deferred_until: null,
          revision: it.revision,
        })
      }
      if (sub === "resolve" && method === "POST") {
        // The operator check comes before the phrase.
        if (store.refuseOperator)
          return fail(
            route,
            403,
            "operator_required",
            "Deciding this request needs operator access.",
          )
        if (!revisionsMatch()) return stale(route, it, body?.expected_revision)
        const phrase = (it.confirm as { phrase?: string } | undefined)?.phrase
        if (
          phrase &&
          body?.response?.decision === "approved" &&
          !body?.response?.alternative_id &&
          body?.response?.confirmation !== phrase
        )
          return fail(route, 422, "confirmation_required", `Type ${phrase} to approve.`)
        store.pending.splice(store.pending.indexOf(it), 1)
        const outcome = {
          item_id: it.item_id,
          state: "resolved",
          resolution: {
            response: body?.response,
            resolved_at: AT,
            participant: {
              principal_ref: "user:owner",
              authority: "operator",
              assurance: "operator-token",
            },
          },
        }
        store.history.unshift({ ...it, state: "resolved", terminal_outcome: outcome })
        return json(route, 200, outcome)
      }
    }
    return fail(
      route,
      404,
      "not_found",
      `the approvals contract stub does not serve ${method} ${path}`,
    )
  })
  return { store }
}

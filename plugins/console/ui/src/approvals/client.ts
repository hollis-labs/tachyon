import { operatorFetch } from "../operator/elevation"

// Tachyon-owned HITL v1 browser projection. Decisions retain the producer's contract;
// the queue remains authoritative for revision checks and immutable outcomes.
export type CardKind = `card.${string}`
export interface CardAnswer {
  questionId: string
  value: unknown
}
export interface ApprovalResponse {
  kind: "approval" | "attention" | CardKind
  decision: "approved" | "denied" | "acknowledged" | "submitted"
  note?: string
  reply?: string
  answers?: CardAnswer[]
}
export interface ApprovalOutcome {
  item_id: string
  state: string
  reason?: string
  message?: string
  resolution?: {
    response: ApprovalResponse
    resolved_at: string
    participant: {
      principal_ref: string
      authority: string
      assurance: string
    }
  }
}
export interface Evidence {
  type: string
  label: string
  content?: string
  [key: string]: unknown
}
export type ActionRunState =
  | "pending_approval"
  | "approved"
  | "waiting_input"
  | "running"
  | "succeeded"
  | "failed"
  | "refused"
  | "denied"
  | "expired"
// A registered action bound to an approval (see README "Registered actions").
export interface ActionRun {
  run_id: string
  item_id: string
  machine: string
  action: string
  argv: string[]
  command: string
  op_digest: string
  class: string
  labels?: string
  state: ActionRunState
  terminal: boolean
  interactive: boolean
  user?: string
  exit_code?: number
  output?: string
  note?: string
  error?: string
  created_at: string
  started_at?: string
  ended_at?: string
}
// What happened after a card with effects was approved.
export interface CardReceipt {
  state: "running" | "completed" | "failed" | "refused" | "none"
  reason?: string
  effects?: { index: number; target: string; op?: string; status: "ok" | "failed" | "skipped"; detail?: string; error?: string }[]
}
export const ACTION_RUNS_BASE = "/api/plugins/console/v1/action-runs"
export interface ApprovalItem {
  action_run?: ActionRun
  card_receipt?: CardReceipt
  item_id: string
  state: string
  revision: number
  presented_projection_revision?: number
  enqueued_at: string
  updated_at: string
  terminal_outcome?: ApprovalOutcome
  // Set while the item is snoozed; it sorts last in the queue until then.
  deferred_until?: string | null
  request_snapshot: {
    kind: "approval" | "attention" | CardKind
    class?: "low" | "medium" | "high"
    // false when the backend says this item cannot be decided in the browser.
    approvable?: boolean
    // card.* requests carry their body in data and may list effects.
    data?: Record<string, unknown>
    effects?: { target: string; [key: string]: unknown }[]
    completion?: "wait" | "async" | "notify"
    op_digest?: string
    handoff_url?: string
    title: string
    summary?: string
    request?: string
    details_markdown?: string
    recommendation?: string
    impact?: { approve: string; deny: string }
    expires_at?: string
    source: {
      application_id: string
      application_label?: string
      agent_id: string
      agent_label?: string
    }
    correlations?: Record<string, unknown>
    evidence?: Evidence[]
  }
}
export interface ApprovalInbox {
  pending: ApprovalItem[]
  history: ApprovalItem[]
  next_pending?: string
  next_history?: string
  synced_at: string
  revision: string
}
export interface DeferResult {
  item_id: string
  deferred_until: string | null
  expires_before_snooze_ends?: boolean
  revision: number
}
export interface SecretStored {
  secret_ref: string
  expires_at: number
  fingerprint: string
}
export const APPROVAL_BASE = "/api/plugins/console/v1/approval-inbox"
export interface HistoryEvent {
  event_id: string
  item_id: string
  kind: string
  at: string
  actor: { ref: string; label: string; assurance?: string }
  decision?: string
  status?: string
  note?: string
  reason?: string
  mini?: { title: string; risk?: string; group?: string }
}
export interface HistoryPage {
  events: HistoryEvent[]
  next_cursor?: string
}
export class ApprovalError extends Error {
  constructor(
    public status: number,
    public code: string,
    message: string,
    public stepUp?: { action?: string },
  ) {
    super(message)
  }
}
// Items the server refused with provider_handoff_required, remembered for the session so the
// operator is not offered (or asked to confirm) the same decision twice.
export const handoffItems = new Set<string>()
export const isHandoffError = (e: unknown) => e instanceof ApprovalError && e.code === "provider_handoff_required"
export const isCard = (item: Pick<ApprovalItem["request_snapshot"], "kind">) =>
  item.kind.startsWith("card.")
// "Approval", "Question", "Form"… for the kind column and search.
export const kindLabel = (kind: string) => {
  const name = kind.startsWith("card.") ? kind.slice(5) : kind
  return name.charAt(0).toUpperCase() + name.slice(1)
}
export const isPending = (item: ApprovalItem) =>
  ["submitted", "validated", "staged", "presented", "in_progress"].includes(
    item.state,
  ) && !item.terminal_outcome
export const isExpired = (item: ApprovalItem) =>
  !!item.request_snapshot.expires_at &&
  Date.parse(item.request_snapshot.expires_at) <= Date.now()

export function createApprovalClient(fetcher: typeof fetch = operatorFetch) {
  let connecting: Promise<unknown> | undefined
  const presenting = new Map<string, Promise<ApprovalItem>>()
  async function request<T>(
    path: string,
    init: RequestInit = {},
    fresh = false,
  ): Promise<T> {
    const res = await fetcher(APPROVAL_BASE + path, {
      ...init,
      headers: {
        Accept: "application/json",
        ...(typeof init.body === "string" ? { "Content-Type": "application/json" } : {}),
        ...(fresh ? { "Cache-Control": "no-cache" } : {}),
        ...init.headers,
      },
    })
    const body = await res.json().catch(() => ({}))
    if (!res.ok)
      throw new ApprovalError(
        res.status,
        body.code ?? "unavailable",
        body.message ?? "The approval queue is unavailable.",
        body.step_up,
      )
    if (
      fresh &&
      ["stale", "error"].includes(res.headers.get("X-Tachyon-Cache") ?? "")
    )
      throw new ApprovalError(
        503,
        "stale",
        "Could not verify the current request. Refresh before deciding.",
      )
    return body as T
  }
  async function read<T>(path: string, fresh = false): Promise<T> {
    try {
      return await request<T>(path, {}, fresh)
    } catch (e) {
      if (!(e instanceof ApprovalError) || e.status !== 401) throw e
      connecting ??= request("/session", { method: "POST" }).finally(() => {
        connecting = undefined
      })
      await connecting
      return request<T>(path, {}, fresh)
    }
  }
  const get = (id: string, fresh = false) =>
    read<ApprovalItem>(`/items/${encodeURIComponent(id)}`, fresh)
  return {
    inbox: (fresh = false, cursors: { pending_before?: string; history_before?: string } = {}) => {
      const query = new URLSearchParams(Object.entries(cursors).filter((entry): entry is [string, string] => !!entry[1]))
      return read<ApprovalInbox>(`/inbox${query.size ? `?${query}` : ""}`, fresh)
    },
    get,
    history(opts: { q?: string; status?: string; cursor?: string; limit?: number } = {}) {
      const query = new URLSearchParams(Object.entries(opts).filter((e): e is [string, string | number] => !!e[1]).map(([k, v]) => [k, String(v)]))
      return read<HistoryPage>(`/history${query.size ? `?${query}` : ""}`, true)
    },
    present(id: string) {
      const existing = presenting.get(id)
      if (existing) return existing
      const pending = (async () => {
        const item = await get(id, true)
        if (!isPending(item) || isExpired(item)) return item
        return request<ApprovalItem>(
          `/items/${encodeURIComponent(id)}/present`,
          {
            method: "POST",
            body: JSON.stringify({
              expected_revision: item.revision,
              presented_projection_revision: item.revision,
              connection_id: "tachyon-browser",
            }),
          },
        )
      })().finally(() => presenting.delete(id))
      presenting.set(id, pending)
      return pending
    },
    // Snooze: the item stays pending but drops to the end of the queue until the time passes.
    defer(item: ApprovalItem, durationS: number, reason?: string) {
      return request<DeferResult>(`/items/${encodeURIComponent(item.item_id)}/defer`, {
        method: "POST",
        body: JSON.stringify({
          expected_revision: item.revision,
          duration_s: durationS,
          ...(reason ? { reason } : {}),
        }),
      })
    },
    undefer(id: string) {
      return request<DeferResult>(`/items/${encodeURIComponent(id)}/defer`, { method: "DELETE" })
    },
    async decide(item: ApprovalItem, response: ApprovalResponse, opts: { keepalive?: boolean } = {}) {
      // A keepalive decision is sent while the page is closing: no time for the pre-check, so the
      // server's expected_revision check is the guard.
      if (opts.keepalive)
        return request<ApprovalOutcome>(
          `/items/${encodeURIComponent(item.item_id)}/resolve`,
          {
            method: "POST",
            keepalive: true,
            body: JSON.stringify({
              expected_revision: item.revision,
              presented_projection_revision: item.presented_projection_revision,
              response,
            }),
          },
        )
      // Compare to the exact projection the person reviewed; never silently
      // re-present newer content and carry their old decision onto it.
      const current = await get(item.item_id, true)
      if (
        !isPending(current) ||
        isExpired(current) ||
        current.revision !== item.revision ||
        !item.presented_projection_revision ||
        current.presented_projection_revision !==
          item.presented_projection_revision
      )
        throw new ApprovalError(
          409,
          "changed",
          "This request changed or has already ended. Refresh and review it again.",
        )
      return request<ApprovalOutcome>(
        `/items/${encodeURIComponent(item.item_id)}/resolve`,
        {
          method: "POST",
          body: JSON.stringify({
            expected_revision: item.revision,
            presented_projection_revision: item.presented_projection_revision,
            response,
          }),
        },
      )
    },
    // Stores a secret for one field of one item and returns only a reference. The value is never
    // returned or kept; the same idempotencyKey on a retry returns the same reference.
    storeSecret(input: { itemId: string; field: string; value?: string; file?: File; idempotencyKey: string }) {
      const headers = { "Idempotency-Key": input.idempotencyKey }
      if (input.file) {
        const form = new FormData()
        form.append("item_id", input.itemId)
        form.append("field", input.field)
        form.append("file", input.file)
        return request<SecretStored>("/secrets", { method: "POST", headers, body: form })
      }
      return request<SecretStored>("/secrets", {
        method: "POST",
        headers,
        body: JSON.stringify({ item_id: input.itemId, field: input.field, value: input.value }),
      })
    },
    evidence: (id: string, index: number, type: string) =>
      request<Record<string, unknown>>(
        `/items/${encodeURIComponent(id)}/evidence/${index}/${type === "tangent_reference" ? "reference" : "preview"}`,
        { headers: { "Cache-Control": "no-store" } },
      ),
  }
}
export type ApprovalClient = ReturnType<typeof createApprovalClient>
export const approvalClient = createApprovalClient()

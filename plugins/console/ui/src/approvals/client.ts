import { operatorFetch } from "../operator/elevation"

// Tangent HITL v1 browser projection. Decisions retain the producer's contract;
// the queue remains authoritative for revision checks and immutable outcomes.
export interface ApprovalResponse {
  kind: "approval" | "attention"
  decision: "approved" | "denied" | "acknowledged"
  note?: string
  reply?: string
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
export interface ApprovalItem {
  item_id: string
  state: string
  revision: number
  presented_projection_revision?: number
  enqueued_at: string
  updated_at: string
  terminal_outcome?: ApprovalOutcome
  request_snapshot: {
    kind: "approval" | "attention"
    title: string
    summary: string
    request: string
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
  synced_at: string
  revision: string
}
export const APPROVAL_BASE = "/api/plugins/console/v1/approval-inbox"
export class ApprovalError extends Error {
  constructor(
    public status: number,
    public code: string,
    message: string,
  ) {
    super(message)
  }
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
        ...(init.body ? { "Content-Type": "application/json" } : {}),
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
    inbox: (fresh = false) => read<ApprovalInbox>("/inbox", fresh),
    get,
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
    async decide(item: ApprovalItem, response: ApprovalResponse) {
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
    evidence: (id: string, index: number, type: string) =>
      request<Record<string, unknown>>(
        `/items/${encodeURIComponent(id)}/evidence/${index}/${type === "tangent_reference" ? "reference" : "preview"}`,
        { headers: { "Cache-Control": "no-store" } },
      ),
  }
}
export type ApprovalClient = ReturnType<typeof createApprovalClient>
export const approvalClient = createApprovalClient()

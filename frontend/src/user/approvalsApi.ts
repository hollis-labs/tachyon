import { hostFetch } from "@tachyon/host-sdk"

// The approvals contract (internal/approvalsapi/openapi.yaml), read-only subset
// the /user screen needs. Authority stays on the server.
const BASE = "/api/plugins/console/v1/approval-inbox"

export interface Access {
  state: "none" | "elevated"
  expires_in_s?: number
  can_elevate: boolean
  ttl_policy?: string
}

export interface DecisionItem {
  item_id: string
  state: string
  updated_at: string
  request_snapshot: {
    title: string
    source: { application_label?: string; agent_label?: string; application_id: string }
  }
}

export interface InboxSummary {
  counts: { pending: number }
  history: DecisionItem[]
}

async function get<T>(path: string): Promise<T> {
  const res = await hostFetch(BASE + path, {
    headers: { Accept: "application/json", "Cache-Control": "no-store" },
  })
  if (!res.ok) throw new Error(`${path} ${res.status}`)
  return (await res.json()) as T
}

export const approvalsApi = {
  access: () => get<Access>("/access"),
  inbox: () => get<InboxSummary>("/inbox?status=history"),
  async deElevate(): Promise<void> {
    const res = await hostFetch(`${BASE}/de-elevate`, {
      method: "POST",
      headers: { "Content-Type": "application/json", Accept: "application/json" },
      body: "{}",
    })
    if (!res.ok) throw new Error("Could not revoke operator access.")
    const body = (await res.json()) as { elevated?: boolean }
    if (body.elevated !== false) throw new Error("Revocation was not confirmed.")
  },
}

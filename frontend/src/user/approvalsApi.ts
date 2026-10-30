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

export interface DecisionEvent {
  event_id: string
  item_id: string
  kind: string
  status: string
  at: string
  mini: { title: string; risk?: string; group?: string }
}

export interface History {
  events: DecisionEvent[]
}

export interface InboxSummary {
  counts: { pending: number }
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
  inbox: () => get<InboxSummary>("/inbox"),
  history: () => get<History>("/history?limit=20"),
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

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

// Dev only: `?user-stub=relaxed|elevated|empty` shows the states while the approvals routes are not live.
function stub<T>(path: string): T | undefined {
  if (!import.meta.env.DEV) return undefined
  const mode = new URLSearchParams(window.location.search).get("user-stub")
  if (!mode) return undefined
  const ago = (s: number) => new Date(Date.now() - s * 1000).toISOString()
  const ev = (id: string, status: string, title: string, group: string, s: number) => ({
    event_id: id,
    item_id: id,
    kind: "approval",
    status,
    at: ago(s),
    mini: { title, group },
  })
  if (path === "/access")
    return (
      mode === "elevated"
        ? { state: "elevated", expires_in_s: 720, can_elevate: true }
        : { state: "none", can_elevate: true }
    ) as T
  if (path === "/inbox") return { counts: { pending: 3 } } as T
  return {
    events:
      mode === "empty"
        ? []
        : [
            ev("1", "approved", "Run migration 029 on staging", "Release bot", 720),
            ev("2", "denied", "Delete 14 stale branches", "Release bot", 7200),
            ev("3", "approved", "Restart the planner service", "Release bot", 100000),
            ev("4", "approved", "Merge pull request 507 into main", "Pull requests", 110000),
          ],
  } as T
}

async function get<T>(path: string): Promise<T> {
  const fake = stub<T>(path.split("?")[0])
  if (fake) return fake
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

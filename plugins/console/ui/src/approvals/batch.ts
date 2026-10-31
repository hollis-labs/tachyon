import type { ApprovalItem, BatchStatus } from "./client"

/** Most requests one batch decision may cover. */
export const MAX_BATCH = 25

// The server's blocker codes (docs/approvals-contract.md, in its canonical order).
const BLOCKER_COPY: Record<string, string> = {
  not_pending: "No longer waiting for a decision.",
  kind_not_batchable: "This kind of request is decided one by one.",
  risk_not_low: "Not low risk: review it on its own.",
  interactive: "Interactive: needs you in the terminal.",
  has_secret: "Needs a secret entered securely.",
  command_exec: "Runs a command: review the exact command.",
  destructive: "Destructive: needs its own confirmation.",
  no_effects_declared: "Does not say what it will change.",
  irreversible_effect: "Cannot be undone.",
  expired: "Already expired.",
  deferred: "Snoozed until later.",
  needs_operator_access: "Needs operator access.",
}

const humanize = (code: string) => {
  const s = code.replace(/[_-]+/g, " ").trim()
  return s ? `${s.charAt(0).toUpperCase()}${s.slice(1)}.` : "Review it on its own."
}

export interface BatchEligibility {
  ok: boolean
  /** Why the request must be reviewed one by one; empty when it can be batched. */
  reasons: string[]
}

/** Whether a request may join a batch decision. The server's `batchable` flag decides; blockers explain a refusal. */
export function batchEligibility(item: Pick<ApprovalItem, "batchable" | "batch_blockers">): BatchEligibility {
  if (item.batchable) return { ok: true, reasons: [] }
  const reasons = (item.batch_blockers ?? []).map((b) => BLOCKER_COPY[b] ?? humanize(b))
  return { ok: false, reasons: reasons.length ? reasons : ["Review it on its own."] }
}

/** GET /access, as the server returns it. */
export interface Access {
  state: "none" | "elevated"
  can_elevate?: boolean
  expires_in_s?: number
  required_for?: { batch?: boolean; medium?: boolean; destructive?: boolean }
}

/** Whether a batch decision is blocked for lack of operator access. An unreadable answer blocks nothing; the server still refuses. */
export const batchNeedsAccess = (a: Pick<Access, "state" | "required_for"> | undefined) => !!a && a.state === "none" && a.required_for?.batch !== false

export const BATCH_STATUS_COPY: Record<BatchStatus, string> = {
  resolved: "Decided",
  stale: "Changed since listed",
  ineligible: "Not eligible",
  expired: "Expired",
  conflict: "Decided elsewhere",
  error: "Failed",
}

const RISK_RANK = { high: 0, medium: 1, low: 2 } as const
const ms = (iso?: string | null, none = Number.POSITIVE_INFINITY) => {
  const t = iso ? Date.parse(iso) : NaN
  return Number.isNaN(t) ? none : t
}

/** Queue order: snoozed last, then soonest expiry, then highest risk, then oldest. */
export function orderPending<T extends Pick<ApprovalItem, "deferred_until" | "enqueued_at" | "request_snapshot">>(items: T[], now = Date.now()): T[] {
  const snoozed = (i: T) => ms(i.deferred_until, 0) > now
  return [...items].sort((a, b) =>
    Number(snoozed(a)) - Number(snoozed(b))
    || ms(a.request_snapshot.expires_at) - ms(b.request_snapshot.expires_at)
    || RISK_RANK[a.request_snapshot.class ?? "medium"] - RISK_RANK[b.request_snapshot.class ?? "medium"]
    || ms(a.enqueued_at) - ms(b.enqueued_at))
}

/**
 * Why an inline (chat) card cannot take this decision on its own, or undefined when it can. The card has no
 * place to type a confirmation phrase or write a reason, so those go to the Approvals page.
 */
export function inlineDecisionBlocker(
  item: Pick<ApprovalItem, "confirm" | "reason_required">,
  response: { decision: string; note?: string },
): string | undefined {
  if (response.decision === "approved" && item.confirm?.phrase) return "This needs a typed confirmation. Open it in Approvals."
  if (response.decision === "denied" && item.reason_required && !response.note?.trim()) return "A reason is needed to deny this. Open it in Approvals."
  return undefined
}

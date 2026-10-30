import type { ApprovalItem, BatchStatus } from "./client"

/** Most requests one batch decision may cover. */
export const MAX_BATCH = 25

const BLOCKER_COPY: Record<string, string> = {
  high_risk: "High risk: review it on its own.",
  destructive: "Destructive: needs its own confirmation.",
  needs_input: "Needs an answer, not a yes or no.",
  needs_secret: "Needs a secret entered securely.",
  deferred: "Snoozed until later.",
  expired: "Already expired.",
  stale: "Changed since it was listed.",
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

export const BATCH_STATUS_COPY: Record<BatchStatus, string> = {
  resolved: "Decided",
  stale: "Changed since listed",
  ineligible: "Not eligible",
  expired: "Expired",
  conflict: "Decided elsewhere",
  error: "Failed",
}

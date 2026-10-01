import type { AskDetail } from "./verbs"

export interface ApprovalStatus {
  operation_id: string
  item_id: string
  state: string
  approved: boolean
  continuation: "unavailable"
  expiry: string
  decision?: string
  resolved_at?: string
}

export class PendingApprovalError extends Error {
  constructor(public readonly ask: AskDetail) {
    super("Operator decision required")
  }
}

export class ApprovalStatusError extends Error {
  constructor(
    public readonly code: string,
    public readonly status: number,
  ) {
    super(code)
  }
}

// This endpoint reads host correlation only. There is deliberately no resume API.
export async function getApprovalStatus(
  operationId: string,
  signal: AbortSignal,
): Promise<ApprovalStatus> {
  const response = await fetch(
    `/api/hitl/operations/${encodeURIComponent(operationId)}?wait_ms=25000`,
    { headers: { Accept: "application/json" }, cache: "no-store", signal },
  )
  const result: unknown = await response.json()
  if (!response.ok) {
    const code =
      typeof result === "object" &&
      result !== null &&
      "error" in result &&
      typeof result.error === "string"
        ? result.error
        : "status_unavailable"
    throw new ApprovalStatusError(code, response.status)
  }
  if (
    typeof result !== "object" ||
    result === null ||
    !("operation_id" in result) ||
    result.operation_id !== operationId ||
    !("item_id" in result) ||
    typeof result.item_id !== "string" ||
    !("state" in result) ||
    typeof result.state !== "string" ||
    !("approved" in result) ||
    typeof result.approved !== "boolean" ||
    !("continuation" in result) ||
    result.continuation !== "unavailable" ||
    !("expiry" in result) ||
    typeof result.expiry !== "string" ||
    !Number.isFinite(Date.parse(result.expiry)) ||
    ("decision" in result && typeof result.decision !== "string") ||
    ("resolved_at" in result && typeof result.resolved_at !== "string")
  )
    throw new ApprovalStatusError("status_unavailable", 502)
  return result as ApprovalStatus
}

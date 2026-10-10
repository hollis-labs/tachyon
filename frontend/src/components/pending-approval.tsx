import { useEffect, useState } from "react"
import { type ApprovalStatus, ApprovalStatusError, getApprovalStatus } from "../api/hitl"
import type { AskDetail } from "../api/verbs"

const waitingStates = new Set(["submitted", "validated", "staged", "presented", "in_progress"])
const terminalLabels: Record<string, string> = {
  canceled: "Canceled",
  expired: "Expired",
  failed: "Failed",
  superseded: "Superseded",
}

function statusLabel(status: ApprovalStatus): string {
  if (status.state === "resolved") {
    if (status.approved && status.decision === "approved") return "Approved"
    if (!status.approved && status.decision === "denied") return "Denied"
    if (!status.approved && status.decision === "acknowledged") return "Attention acknowledged"
    return "Decision unknown"
  }
  return (
    terminalLabels[status.state] ??
    (waitingStates.has(status.state) ? "Awaiting operator decision" : "Decision unknown")
  )
}

function safeItemLink(value: unknown): string | undefined {
  if (typeof value !== "string") return
  try {
    const url = new URL(value)
    if (["http:", "https:"].includes(url.protocol) && !url.username && !url.password) return value
  } catch {
    /* Missing/invalid links remain plain text. */
  }
}

export function PendingApproval({ ask }: { ask: AskDetail }) {
  const [label, setLabel] = useState("Awaiting operator decision")
  const [expiry, setExpiry] = useState(ask.expiry)
  useEffect(() => {
    let active = true
    let timer: ReturnType<typeof setTimeout> | undefined
    let deadlineTimer: ReturnType<typeof setTimeout> | undefined
    const controller = new AbortController()
    let failures = 0
    // A mounted panel never polls indefinitely, even for an unconfigured/raw ask.
    const suppliedExpiry = Date.parse(ask.expiry ?? "")
    const deadline = Math.min(
      Number.isFinite(suppliedExpiry) ? suppliedExpiry : Infinity,
      Date.now() + 600000,
    )
    setExpiry(ask.expiry)
    setLabel("Awaiting operator decision")
    function stop(value: string) {
      if (!active) return
      setLabel(value)
      active = false
      clearTimeout(timer)
      clearTimeout(deadlineTimer)
      controller.abort()
    }
    async function poll() {
      if (!active || !ask.operation_id) return
      if (Date.now() >= deadline) {
        stop("Tracking window expired")
        return
      }
      try {
        const status = await getApprovalStatus(ask.operation_id, controller.signal)
        if (!active) return
        if (ask.item_id && status.item_id !== ask.item_id) {
          stop("Decision unknown")
          return
        }
        failures = 0
        setExpiry(status.expiry)
        setLabel(statusLabel(status))
        if (!waitingStates.has(status.state)) {
          stop(statusLabel(status))
          return
        }
        if (Date.parse(status.expiry) <= Date.now()) {
          stop("Expired")
          return
        }
        // An immediate await response still waits before the next GET.
        timer = setTimeout(poll, 3000)
      } catch (error) {
        if (!active) return
        if (error instanceof ApprovalStatusError && error.status === 410) {
          stop("No longer tracked")
          return
        }
        if (error instanceof ApprovalStatusError && error.status === 403) {
          stop("Status access rejected")
          return
        }
        failures++
        setLabel(
          error instanceof ApprovalStatusError && error.code === "poll_busy"
            ? "Status check busy; retrying later"
            : "Host status unavailable; retrying later",
        )
        if (failures >= 5) {
          stop("Host status unavailable; polling stopped")
          return
        }
        timer = setTimeout(poll, Math.min(30000, 3000 * 2 ** (failures - 1)))
      }
    }
    if (!ask.operation_id || ask.state === "unavailable") stop("Host tracking unavailable")
    else if (deadline <= Date.now()) stop("Tracking window expired")
    else {
      deadlineTimer = setTimeout(() => stop("Tracking window expired"), deadline - Date.now())
      void poll()
    }
    return () => {
      active = false
      clearTimeout(timer)
      clearTimeout(deadlineTimer)
      controller.abort()
    }
  }, [ask])
  const link = safeItemLink(ask.item_url)
  return (
    <section
      aria-label="Pending operator decision"
      className="my-4 min-w-0 space-y-3 rounded-md border border-border bg-surface p-4 text-sm"
    >
      <h3 className="font-medium">Operator decision</h3>
      <p className="whitespace-pre-wrap break-words">
        {ask.prompt || "An operator decision was requested."}
      </p>
      <p role="status" aria-live="polite" aria-atomic="true">
        {label}
      </p>
      <p>
        Continuation is unavailable. No action was taken by Tachyon in response to this decision.
        Start a new action if it is still wanted.
      </p>
      {ask.options?.length ? (
        <div>
          <p className="text-text-muted">Requested options (read only)</p>
          <ul className="list-inside list-disc break-words">
            {ask.options.map((option, index) => (
              // immutable read-only options may contain duplicate labels.
              <li key={`${index}-${option}`}>{option}</li>
            ))}
          </ul>
        </div>
      ) : null}
      {ask.context && (
        <div>
          <p className="text-text-muted">Context (read only)</p>
          <pre className="max-h-48 overflow-auto whitespace-pre-wrap break-all text-xs">
            {JSON.stringify(ask.context, null, 2)}
          </pre>
        </div>
      )}
      {ask.item_id && <p className="break-all">Item: {ask.item_id}</p>}
      {expiry && <p className="break-all">Tracking expiry: {expiry}</p>}
      {link && (
        <a
          href={link}
          target="_blank"
          rel="noopener noreferrer"
          className="inline-block text-primary underline"
        >
          Open in Tangent
        </a>
      )}
    </section>
  )
}

import type {
  DiagnosticValidation,
  DiagnosticValue,
  HealthStatus,
  ObservationState,
  StatObservation,
} from "@hollis-labs/kit-observe"
import type { StatusSummary } from "../../api/observe"

export const STALE_AFTER_MS = 30000

export function statusSnapshot(value: StatusSummary): StatusSummary {
  if (!value || typeof value !== "object" || Array.isArray(value))
    throw new Error("Malformed Observe status snapshot.")
  return value
}

export function snapshotRows<T>(value: T[] | null, fields: string[]): T[] | null {
  if (value === null) return null
  if (
    !Array.isArray(value) ||
    value.length > 200 ||
    value.some(
      (row) =>
        !row ||
        typeof row !== "object" ||
        Array.isArray(row) ||
        fields.some((key) => typeof (row as Record<string, unknown>)[key] !== "string") ||
        ("unit" in row && row.unit !== undefined && typeof row.unit !== "string") ||
        ("tags" in row &&
          row.tags !== undefined &&
          (!row.tags ||
            typeof row.tags !== "object" ||
            Array.isArray(row.tags) ||
            Object.values(row.tags).some((tag) => typeof tag !== "string"))),
    )
  )
    throw new Error("Malformed bounded Observe snapshot.")
  return value
}

export function observationState(
  read: { loading: boolean; error: string; updated?: string; ask?: unknown; auto?: boolean },
  nowMs: number,
  supported = true,
  observedAt = read.updated,
): ObservationState {
  return {
    phase: read.loading ? "loading" : read.error ? "error" : read.updated ? "ready" : "idle",
    observedAt,
    nowMs,
    staleAfterMs: STALE_AFTER_MS,
    supported,
    paused: !!read.ask || !read.auto,
    error: read.error ? "The Observe read failed. Refresh explicitly to try again." : undefined,
  }
}

export function reportedHealth(value: unknown): HealthStatus {
  return value === "healthy" || value === "degraded" || value === "unhealthy" ? value : "unknown"
}

export function reachability(value: unknown): HealthStatus {
  return value === "reachable" ? "healthy" : value === "unreachable" ? "unhealthy" : "unknown"
}

const count = (value: unknown): number | null =>
  typeof value === "number" && Number.isSafeInteger(value) && value >= 0 ? value : null

export function statusStats(
  status: StatusSummary,
  observation: ObservationState,
): StatObservation[] {
  const sessions = status.session_count_known === true ? count(status.active_sessions) : null
  return [
    {
      id: "uptime",
      label: "Observe process uptime",
      value:
        typeof status.uptime_seconds === "number" &&
        Number.isFinite(status.uptime_seconds) &&
        status.uptime_seconds >= 0
          ? status.uptime_seconds
          : null,
      unit: "seconds",
    },
    {
      id: "sessions",
      label: `Nanite sessions with status active${sessions === null ? " — Unknown" : ""}`,
      value: sessions,
      unit: "count",
    },
    { id: "agents", label: "Active agents — Unknown — not measured", value: null, unit: "count" },
    {
      id: "errors",
      label: "Local error events and plugin failures (retained ring)",
      value: count(status.error_count),
      unit: "count",
    },
    {
      id: "operations",
      label: "Operation errors (retained ring; separate from health)",
      value: count(status.operation_error_count),
      unit: "count",
    },
  ].map((row) => ({ ...row, unit: row.unit as "seconds" | "count", kind: "gauge", observation }))
}

// Only metadata fields from the snapshot/feed contract enter copyable diagnostics.
const textFields = [
  "id",
  "timestamp",
  "kind",
  "plugin",
  "generation",
  "operation",
  "operation_id",
  "module",
  "verb",
  "method",
  "effect",
  "status",
  "reason",
  "reason_class",
  "source",
  "checked_at",
  "session_id",
  "provider",
  "model",
  "project_id",
  "epoch",
]
const numberFields = [
  "sequence",
  "duration_ms",
  "last_sequence",
  "overflow",
  "lifecycle_overflow",
  "unavailable",
  "delivery_failed",
  "delivered",
  "queue_wait_timeouts",
]
const properties: Record<string, DiagnosticValue> = Object.fromEntries([
  ...textFields.map((key) => [
    key,
    { type: "string", maxLength: 256, pattern: "^[a-zA-Z0-9_.:/-]*$" },
  ]),
  ...numberFields.map((key) => [
    key,
    { type: "integer", minimum: 0, maximum: Number.MAX_SAFE_INTEGER },
  ]),
  ["error_reported", { type: "boolean" }],
  ["omitted_fields", { type: "integer", minimum: 0, maximum: 64 }],
])
const metadataSchema: DiagnosticValue = {
  type: "object",
  properties,
  additionalProperties: false,
  maxProperties: 64,
}
export const diagnosticSchema: DiagnosticValue = {
  ...metadataSchema,
  properties: { ...properties, counters: metadataSchema },
}

export function diagnosticProjection(value: unknown): {
  data: DiagnosticValue
  validation: DiagnosticValidation
} {
  let nodes = 0
  function project(input: unknown, depth: number): Record<string, DiagnosticValue> {
    if (!input || typeof input !== "object" || Array.isArray(input) || depth > 1)
      throw new Error("Diagnostic metadata must be a bounded object.")
    const entries = Object.entries(input)
    if (entries.length > 64) throw new Error("Diagnostic metadata exceeds the field bound.")
    const output: Record<string, DiagnosticValue> = {}
    let omitted = 0
    for (const [key, item] of entries) {
      if (++nodes > 128) throw new Error("Diagnostic metadata exceeds the size bound.")
      if (key === "counters" && depth === 0) output.counters = project(item, depth + 1)
      else if (key === "error") output.error_reported = !!item
      else if (textFields.includes(key)) {
        if (
          typeof item !== "string" ||
          item.length > 256 ||
          !/^[a-zA-Z0-9_.:/-]*$/.test(item) ||
          item.includes("://")
        )
          throw new Error("Diagnostic metadata contains an invalid identifier or timestamp.")
        output[key] = item
      } else if (numberFields.includes(key)) {
        const checked = count(item)
        if (checked === null) throw new Error("Diagnostic metadata contains an invalid count.")
        output[key] = checked
      } else omitted++
    }
    if (omitted) output.omitted_fields = omitted
    return output
  }
  try {
    const data = project(value, 0)
    if (Object.keys(data).length === 1 && "omitted_fields" in data)
      return {
        data: null,
        validation: {
          state: "unsupported",
          messages: ["No recognized diagnostic metadata fields were supplied."],
        },
      }
    if (JSON.stringify(data).length > 16384)
      throw new Error("Diagnostic metadata exceeds the byte bound.")
    return { data, validation: { state: "valid" } }
  } catch (error) {
    return {
      data: null,
      validation: {
        state: "invalid",
        messages: [error instanceof Error ? error.message : "Invalid diagnostic metadata."],
      },
    }
  }
}

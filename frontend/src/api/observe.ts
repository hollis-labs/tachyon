import { PendingApprovalError } from "./hitl"
import { type Envelope, invokeVerb } from "./verbs"

export interface ActivityEntry {
  id: string
  timestamp: string
  kind: string
  source: string
  actor: string
  summary: string
  detail?: unknown
}
export interface LogEntry {
  id: string
  timestamp: string
  level: string
  source: string
  message: string
  fields?: Record<string, unknown>
}
export interface MetricPoint {
  name: string
  value: number
  timestamp: string
  unit?: string
  tags?: Record<string, string>
}
export interface ObserveEvent {
  id: string
  timestamp: string
  kind: string
  source: string
  payload?: Record<string, unknown>
}
export interface DependencyStatus {
  source: string
  status: string
  checked_at: string
  error?: string
}
export interface HostFeedStatus {
  epoch: string
  last_sequence: number
  counters: {
    overflow: number
    lifecycle_overflow: number
    unavailable: number
    delivery_failed: number
    delivered: number
    queue_wait_timeouts: number
  }
}
export interface StatusSummary {
  host_feed?: HostFeedStatus
  operation_error_count: number
  error_count: number
  dependencies?: DependencyStatus[]
  session_count_known: boolean
  active_agents: number
  active_sessions: number
  health_status: string
  uptime_seconds: number
  last_updated: string
}
export interface ActivityFilter {
  source?: string
  kind?: string
  limit?: number
  since_id?: string
}
export interface LogFilter {
  source?: string
  level?: string
  search?: string
  since?: string
  until?: string
  limit?: number
}
export interface MetricFilter {
  names?: string[]
  tags?: Record<string, string>
  since?: string
  until?: string
  limit?: number
}
export type PollChannel = "activity" | "logs" | "events"
export type SnapshotFilter = ActivityFilter | LogFilter
export interface SubscriptionHandle {
  channel: string
  endpoint: string
  method: string
  payload: Record<string, unknown>
  transport: string
  supported: boolean
  mode: string
  poll_interval_ms: number
  max_limit: number
  cursor_supported: boolean
  cursor: unknown
  durable_replay: boolean
}
export const observeApi = {
  activity: (filter: ActivityFilter) =>
    invokeVerb<ActivityEntry[] | null>("observe_activity", filter),
  events: (filter: ActivityFilter) => invokeVerb<ObserveEvent[] | null>("observe_events", filter),
  logs: (filter: LogFilter) => invokeVerb<LogEntry[] | null>("observe_logs", filter),
  metrics: (filter: MetricFilter) => invokeVerb<MetricPoint[] | null>("observe_metrics", filter),
  status: () => invokeVerb<StatusSummary>("observe_status"),
  subscribe: (channel: PollChannel, filter: SnapshotFilter) =>
    invokeVerb<SubscriptionHandle>("observe_subscribe", {
      channel,
      filter: JSON.stringify(filter),
    }),
  snapshot: <T>(channel: PollChannel, payload: Record<string, unknown>) =>
    invokeVerb<T>(`observe_${channel}`, payload),
}
export function observeData<T>(envelope: Envelope<T>): T {
  if (envelope.status === "ok") return envelope.data
  if (envelope.status === "ask") throw new PendingApprovalError(envelope.ask)
  throw new Error(envelope.error.message)
}

// Only the declared same-origin snapshot contract is executable. Never follow a URL.
export function validateDescriptor(
  value: SubscriptionHandle,
  channel: PollChannel,
): SubscriptionHandle {
  if (
    !value ||
    value.channel !== channel ||
    value.endpoint !== `/api/verb/observe_${channel}` ||
    value.method !== "POST" ||
    value.supported !== true ||
    value.transport !== "polling" ||
    value.mode !== "snapshot" ||
    value.cursor_supported !== false ||
    value.cursor !== null ||
    value.durable_replay !== false ||
    !Number.isSafeInteger(value.poll_interval_ms) ||
    value.poll_interval_ms < 1 ||
    value.poll_interval_ms > 600000 ||
    !Number.isSafeInteger(value.max_limit) ||
    value.max_limit < 1 ||
    value.max_limit > 200 ||
    !value.payload ||
    typeof value.payload !== "object" ||
    Array.isArray(value.payload)
  )
    throw new Error(
      "Snapshot polling is unsupported or its descriptor is invalid. Manual Refresh remains available.",
    )
  const allowed =
    channel === "logs"
      ? ["source", "level", "search", "since", "until", "limit"]
      : ["source", "kind", "limit"]
  for (const [key, item] of Object.entries(value.payload)) {
    if (!allowed.includes(key) || (key !== "limit" && typeof item !== "string"))
      throw new Error("Invalid snapshot filter descriptor. Manual Refresh remains available.")
    if ((key === "since" || key === "until") && item && !Number.isFinite(Date.parse(String(item))))
      throw new Error("Invalid snapshot timestamp descriptor. Manual Refresh remains available.")
  }
  const limit = value.payload.limit
  if (typeof limit !== "number" || !Number.isInteger(limit) || limit < 1 || limit > value.max_limit)
    throw new Error("Invalid snapshot limit descriptor. Manual Refresh remains available.")
  return value
}

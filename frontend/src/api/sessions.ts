import { type Envelope, invokeVerb } from "./verbs"

export interface ProviderSession {
  id: string
  launch_id: string
  agent_id: string
  project_id: string
  provider_id: string
  provider_kind?: string
  state: string
  created_at: string
  updated_at: string
  ended_at?: string | null
}
export interface SessionPage {
  sessions: ProviderSession[]
  next_cursor?: string
}
export interface SessionFilters {
  agent_id?: string
  status?: string
  provider?: string
  limit?: number
  cursor?: string
}
export interface CreateSessionRequest {
  launch_id: string
  boot_prompt?: string
  idempotency_key?: string
}
export interface ConnectionInfo {
  session_id: string
  provider_id: string
  state: string
  transport: string
  streaming: boolean
}
export interface HistoryEvent {
  seq: number
  timestamp: string
  scope: string
  kind: string
  session_id?: string
  payload_json?: string
}
export interface HistoryPage {
  events: HistoryEvent[]
  next_cursor?: number
}
export interface HistoryRequest {
  id: string
  limit?: number
  cursor?: number
  since_seq?: number
}
function record(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === "object" && !Array.isArray(value)
}
function session(value: unknown): boolean {
  return (
    record(value) &&
    [
      "id",
      "launch_id",
      "agent_id",
      "project_id",
      "provider_id",
      "state",
      "created_at",
      "updated_at",
    ].every((key) => typeof value[key] === "string") &&
    (value.provider_kind === undefined || typeof value.provider_kind === "string") &&
    (value.ended_at == null || typeof value.ended_at === "string")
  )
}
async function call<T>(
  verb: string,
  payload: unknown,
  valid: (value: unknown) => boolean,
): Promise<Envelope<T>> {
  const result = await invokeVerb<T>(verb, payload)
  if (result.status === "ok" && !valid(result.data))
    return {
      status: "error",
      error: {
        code: "invalid_response",
        message: "The provider returned incomplete session data.",
      },
    }
  return result
}
function sessionPage(value: unknown): boolean {
  return (
    record(value) &&
    Array.isArray(value.sessions) &&
    value.sessions.every(session) &&
    (value.next_cursor === undefined || typeof value.next_cursor === "string")
  )
}
function historyPage(value: unknown): boolean {
  return (
    record(value) &&
    Array.isArray(value.events) &&
    value.events.every(
      (event) =>
        record(event) &&
        Number.isSafeInteger(event.seq) &&
        ["timestamp", "scope", "kind"].every((key) => typeof event[key] === "string") &&
        (event.payload_json === undefined || typeof event.payload_json === "string"),
    ) &&
    (value.next_cursor === undefined ||
      (Number.isSafeInteger(value.next_cursor) && Number(value.next_cursor) >= 0))
  )
}
export const sessionsApi = {
  list: (request: SessionFilters) => call<SessionPage>("session_list", request, sessionPage),
  read: (id: string) => call<ProviderSession>("session_read", { id }, session),
  attach: (id: string) =>
    call<ConnectionInfo>(
      "session_attach",
      { id },
      (value) =>
        record(value) &&
        ["session_id", "provider_id", "state", "transport"].every(
          (key) => typeof value[key] === "string",
        ) &&
        typeof value.streaming === "boolean",
    ),
  history: (request: HistoryRequest) => call<HistoryPage>("session_history", request, historyPage),
  create: (request: CreateSessionRequest) =>
    call<ProviderSession>("session_create", request, session),
  stop: (id: string) =>
    call<{ id: string; stopped: boolean }>(
      "session_stop",
      { id },
      (value) => record(value) && value.id === id && value.stopped === true,
    ),
  submit: (id: string, text: string) =>
    call<{ id: string; submitted: boolean }>(
      "session_submit",
      { id, text },
      (value) => record(value) && value.id === id && value.submitted === true,
    ),
}
export function sessionTime(value?: string | null) {
  const stamp = Date.parse(value ?? "")
  return Number.isFinite(stamp) ? new Date(stamp).toLocaleString() : "Not available"
}

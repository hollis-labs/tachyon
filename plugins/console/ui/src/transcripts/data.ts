import { hostFetch } from "@tachyon/host-sdk"
import { APPROVAL_BASE } from "../approvals/client"
import { operatorFetch } from "../operator/elevation"

export interface Session {
  id: string
  tool: string
  kind: string
  model?: string
  project?: string
  title: string
  started_at?: string
  ended_at?: string
  turns: number
  tokens: number
  tool_calls: number
  errors: number
  enriched?: boolean
  tags?: string[]
}
export interface Snapshot {
  status: {
    available: boolean
    enabled: boolean
    semantic: boolean
    modes: string[]
  }
  facets: {
    total: number
    enriched: number
    project_count: number
    tools: string[]
    models: string[]
  }
  sessions: Session[]
  complete: boolean
  limit: number
  observed_at: string
}
/** Per-turn tool call. target and error_excerpt arrive later than the rest; all text is untrusted. */
export interface ToolCall {
  name: string
  status?: "ok" | "error" | "none"
  result_bytes?: number
  started_at?: number
  ended_at?: number
  target?: string
  error_excerpt?: string
}
export const failed = (t: { tool_calls?: ToolCall[] }) =>
  !!t.tool_calls?.some((c) => c.status === "error")
export function size(bytes = 0) {
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1048576) return `${(bytes / 1024).toFixed(1)} KB`
  return `${(bytes / 1048576).toFixed(1)} MB`
}
export function took(c: ToolCall) {
  if (c.started_at == null || c.ended_at == null || c.ended_at < c.started_at) return ""
  const ms = c.ended_at - c.started_at
  return ms < 1000 ? `${ms} ms` : `${(ms / 1000).toFixed(1)} s`
}
/** Accepts both the current field names and the contract's (turn_count, ms timestamps). */
export function normalizeSession(raw: Record<string, unknown>): Session {
  const n = (...k: string[]) => {
    for (const x of k) if (typeof raw[x] === "number") return raw[x] as number
    return 0
  }
  const when = (v: unknown) =>
    typeof v === "number" ? new Date(v).toISOString() : typeof v === "string" ? v : undefined
  return {
    ...(raw as unknown as Session),
    started_at: when(raw.started_at),
    ended_at: when(raw.ended_at),
    turns: n("turns", "turn_count"),
    tool_calls: n("tool_calls", "tool_call_count"),
    errors: n("errors", "error_count"),
    tokens: n("tokens"),
    tool: typeof raw.tool === "string" ? raw.tool : Array.isArray(raw.tools) ? String(raw.tools[0] ?? "") : "",
  }
}
export interface Detail {
  session: Session
  usage?: {
    requests: number
    input_tokens: number
    output_tokens: number
    cache_read_tokens: number
    cache_creation_tokens: number
  }
  turns: {
    seq: number
    role: string
    ts?: string
    text?: string
    tool_calls?: ToolCall[]
  }[]
  turn_total: number
  next_turn?: number
  subagents?: unknown[]
}
export const BASE = "/api/plugins/console/v1/transcripts"
export class TranscriptError extends Error {
  constructor(
    public status: number,
    public code: string,
    message: string,
  ) {
    super(message)
  }
}
// Transcripts share the operator session with the Approvals page (transcripts.read guard).
let connecting: Promise<unknown> | undefined
export async function connectSession() {
  connecting ??= operatorFetch(APPROVAL_BASE + "/session", { method: "POST" })
    .then((res) => {
      if (!res.ok) throw new TranscriptError(res.status, "session_required", "Connect the approval queue first.")
    })
    .finally(() => {
      connecting = undefined
    })
  await connecting
}
export async function read<T>(
  path: string,
  force = false,
  signal?: AbortSignal,
  retried = false,
): Promise<T> {
  const res = await hostFetch(BASE + path, {
    headers: {
      Accept: "application/json",
      ...(force ? { "Cache-Control": "no-cache" } : {}),
    },
    signal,
  })
  const body = await res.json().catch(() => null)
  if (res.status === 401 && body?.code === "session_required" && !retried) {
    // Same quiet connect the Approvals page does; if it fails the page asks the operator.
    try {
      await connectSession()
    } catch {
      throw new TranscriptError(401, "session_required", body.message ?? "Connect the approval queue first.")
    }
    return read<T>(path, force, signal, true)
  }
  if (!res.ok)
    throw new TranscriptError(
      res.status,
      body?.code ?? "",
      body?.message ?? `Transcript read failed (${res.status}).`,
    )
  if (!body)
    throw new Error("The transcript source returned an invalid response.")
  return body as T
}
export interface Filters {
  days: number
  tool: string
  model: string
  kind: string
  errors: boolean
  query: string
}
export function filterSessions(
  rows: Session[],
  filter: Filters,
  now = Date.now(),
) {
  const query = filter.query.trim().toLowerCase()
  // UTC calendar days match the dashboard chart's daily buckets.
  const cutoff =
    Date.parse(new Date(now).toISOString().slice(0, 10)) -
    (filter.days - 1) * 86400000
  // A one-day window is the last 24 hours, not "since UTC midnight".
  const from = filter.days === 1 ? now - 86400000 : cutoff
  return rows.filter(
    (s) =>
      (!filter.days ||
        (!!s.started_at &&
          Date.parse(s.started_at) >= from &&
          Date.parse(s.started_at) <= now)) &&
      (!filter.tool || s.tool === filter.tool) &&
      (!filter.model || s.model === filter.model) &&
      (!filter.kind || s.kind === filter.kind) &&
      (!filter.errors || s.errors > 0) &&
      (!query ||
        [s.title, s.project, s.model, s.tool, ...(s.tags ?? [])]
          .join(" ")
          .toLowerCase()
          .includes(query)),
  )
}
export function summarize(rows: Session[]) {
  return rows.reduce(
    (a, s) => ({
      turns: a.turns + s.turns,
      tokens: a.tokens + s.tokens,
      calls: a.calls + s.tool_calls,
      errors: a.errors + s.errors,
      affected: a.affected + (s.errors > 0 ? 1 : 0),
    }),
    { turns: 0, tokens: 0, calls: 0, errors: 0, affected: 0 },
  )
}
export function rank(rows: Session[], field: "tool" | "model" | "project") {
  const groups = new Map<string, number>()
  for (const s of rows) {
    const label = s[field] || "Unspecified"
    groups.set(label, (groups.get(label) ?? 0) + 1)
  }
  return [...groups]
    .map(([label, value]) => ({ label, value }))
    .sort((a, b) => b.value - a.value || a.label.localeCompare(b.label))
}
export const number = (n: number) => new Intl.NumberFormat().format(n)
export const compact = (n: number) =>
  new Intl.NumberFormat(undefined, {
    notation: "compact",
    maximumFractionDigits: 1,
  }).format(n)
export const projectName = (p?: string) =>
  p?.replace(/\/$/, "").split("/").slice(-2).join("/") || "Unspecified"

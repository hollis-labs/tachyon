import { hostFetch } from "@tachyon/host-sdk"

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
export interface Detail {
  session: Session
  usage?: {
    requests: number
    input_tokens: number
    output_tokens: number
    cache_read_tokens: number
    cache_creation_tokens: number
  }
  turns: { seq: number; role: string; ts?: string; text?: string }[]
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
export async function read<T>(
  path: string,
  force = false,
  signal?: AbortSignal,
): Promise<T> {
  const res = await hostFetch(BASE + path, {
    headers: {
      Accept: "application/json",
      ...(force ? { "Cache-Control": "no-cache" } : {}),
    },
    signal,
  })
  const body = await res.json().catch(() => null)
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
  return rows.filter(
    (s) =>
      (!filter.days ||
        (!!s.started_at &&
          Date.parse(s.started_at) >= cutoff &&
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
